package gb28181

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

// upperUDP is TransportUDP normalised the way outbound requests need it: sipgo wants
// the transport name in upper case, while device.Transport is stored lower case.
const upperUDP = "UDP"

// sipTagBytes is the entropy behind the From tag. 64 bits keeps collision odds
// negligible across a process lifetime, which is all tag correlation needs.
const sipTagBytes = 8

// handlerDrainPoll is how often Stop checks whether in-flight request handlers have
// finished. sipgo runs every request on its own goroutine, which the serve WaitGroup
// does not track.
const handlerDrainPoll = 5 * time.Millisecond

// IngressStats is a point-in-time view of the inbound and signalling protections.
// Every counter is exact; only the *log lines* are sampled (see logThrottle), so the
// snapshot answers "is the cap firing in the field" without the log answering per
// packet.
type IngressStats struct {
	OversizedMessages uint64
	RejectedConns     uint64
	InboundLimited    uint64
	OutboundLimited   uint64
	ActiveConns       int
}

// sipIngress is the SIP UAS that accepts device registrations and keepalives.
//
// It deliberately stops at signalling: catalog sync (S2) and playback (S3) extend
// this type, they do not replace it. Nothing here touches media bytes or the native
// engine, which is what keeps the signalling layer testable without hardware.
type sipIngress struct {
	store   *Store
	runtime *Runtime
	logger  *zap.Logger
	limits  ingressLimits

	mu      sync.Mutex
	started bool

	cfg          Config
	advertisedIP string
	nonces       *nonceManager
	limiter      *rateLimiter
	outbound     *rateLimiter
	throttle     *logThrottle
	devices      *deviceStateMachine
	catalogs     *CatalogManager

	ua       *sipgo.UserAgent
	server   *sipgo.Server
	client   *sipgo.Client
	udpConn  net.PacketConn
	tcpLn    *ingressListener
	serveWG  sync.WaitGroup
	sweepOff context.CancelFunc
	sweepWG  sync.WaitGroup

	// inflight counts request handlers sipgo is running right now. Stop waits for it
	// so a handler cannot write to the database after the host closed it.
	inflight atomic.Int64

	// oversizedDrops counts datagrams refused by the size cap so the operator can see
	// that inbound protection is firing in the field.
	oversizedDrops atomic.Uint64
}

// Stats reports the protection counters. It is safe before Start: everything reads
// as zero.
func (i *sipIngress) Stats() IngressStats {
	stats := IngressStats{OversizedMessages: i.oversizedDrops.Load()}

	i.mu.Lock()
	limiter, outbound, listener := i.limiter, i.outbound, i.tcpLn
	i.mu.Unlock()

	if limiter != nil {
		stats.InboundLimited = limiter.Dropped()
	}
	if outbound != nil {
		stats.OutboundLimited = outbound.Dropped()
	}
	if listener != nil {
		stats.RejectedConns = listener.Rejected()
		stats.ActiveConns = listener.Active()
	}
	return stats
}

// newSIPIngress constructs the ingress. It performs no I/O: sockets are bound in
// Start, which the host calls from its lifecycle.
func newSIPIngress(store *Store, runtime *Runtime, logger *zap.Logger, limits ingressLimits) *sipIngress {
	if logger == nil {
		logger = zap.NewNop()
	}
	limits = limits.withDefaults()
	ingress := &sipIngress{store: store, runtime: runtime, logger: logger, limits: limits}
	ingress.catalogs = NewCatalogManager(ingress, store, logger)
	return ingress
}

// Start binds the SIP listeners described by cfg and begins serving.
//
// Callers get an error for anything that is really an error (unreadable store, no
// bindable address, failing UA construction). A listener that binds and then dies is
// reported through the runtime state instead, because the web console must stay up.
//
// Starting an already running ingress is an error rather than a silent no-op: a
// caller that asks for a configuration the running process cannot adopt must not be
// told it succeeded.
func (i *sipIngress) Start(ctx context.Context, cfg Config, advertisedIP string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.started {
		return apperr.Precondition(CodeRuntimeNotReady,
			"gb28181 sip ingress is already running; restart the process to apply a new configuration", nil)
	}
	if i.store == nil {
		return errors.New("gb28181 ingress requires a device store")
	}
	i.cfg = cfg
	i.advertisedIP = advertisedIP

	if err := i.initialize(); err != nil {
		return err
	}
	if err := i.buildStack(); err != nil {
		return err
	}
	if err := i.bindListeners(cfg); err != nil {
		i.rollbackStack(ctx)
		return err
	}
	i.startSweeper()

	i.started = true
	i.logger.Info("gb28181 sip ingress listening",
		zap.String("sip_listen", cfg.SIPListen),
		zap.String("transport", cfg.SIPTransport),
		zap.String("realm", cfg.SIPRealm),
		zap.String("advertised_ip", advertisedIP),
	)
	return nil
}

// initialize seeds the per-process state a bound listener depends on: the nonce
// secret, the inbound and outbound limiters, and the device state machine.
//
// It does not clear leftover registrations: that is a lifecycle step, because it has
// to happen even when the subsystem is disabled (see LifecycleManager.Start).
func (i *sipIngress) initialize() error {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("seed gb28181 nonce secret: %w", err)
	}
	i.nonces = newNonceManager(secret, i.limits.NonceTTL)
	i.limiter = newRateLimiter(i.limits.RateBurst, i.limits.RateRefill)
	i.outbound = newRateLimiter(i.limits.OutboundBurst, i.limits.OutboundRefill)
	i.throttle = newLogThrottle(i.limits.LogThrottleWindow, defaultLogThrottleKeys)
	i.devices = newDeviceStateMachine(deviceStateOptions{
		Store:             i.store,
		Runtime:           i.runtime,
		Logger:            i.logger,
		KeepaliveTimeout:  i.limits.KeepaliveTimeout,
		RegistrationGrace: i.limits.RegistrationGrace,
		SweepInterval:     i.limits.SweepInterval,
	})
	return nil
}

// buildStack constructs the sipgo user agent and server with the inbound
// protections wired in.
func (i *sipIngress) buildStack() error {
	ua, err := sipgo.NewUA(
		sipgo.WithUserAgent("Zhulong"),
		sipgo.WithUserAgentTransactionLayerOptions(sip.WithTransactionLayerTerminateOnConnClose()),
		sipgo.WithUserAgentTransportLayerOptions(
			sip.WithTransportLayerReadFilter(i.readFilter),
		),
	)
	if err != nil {
		return fmt.Errorf("create sip user agent: %w", err)
	}
	server, err := sipgo.NewServer(ua)
	if err != nil {
		closeQuietly(i.logger, ua, "sip user agent")
		return fmt.Errorf("create sip server: %w", err)
	}
	server.OnRegister(i.tracked(i.handleRegister))
	server.OnMessage(i.tracked(i.handleMessage))
	server.OnOptions(i.tracked(i.handleOptions))
	server.OnNoRoute(i.tracked(i.handleUnsupported))

	client, err := sipgo.NewClient(ua, sipgo.WithClientHostname(i.advertisedIP))
	if err != nil {
		closeQuietly(i.logger, server, "sip server")
		closeQuietly(i.logger, ua, "sip user agent")
		return fmt.Errorf("create sip client: %w", err)
	}

	i.ua = ua
	i.server = server
	i.client = client
	return nil
}

// tracked wraps a request handler so Stop can wait for the handlers sipgo is running
// on their own goroutines. Without it, a REGISTER being handled at shutdown can reach
// the database after the host closed it.
func (i *sipIngress) tracked(fn sipgo.RequestHandler) sipgo.RequestHandler {
	return func(req *sip.Request, tx sip.ServerTransaction) {
		i.inflight.Add(1)
		defer i.inflight.Add(-1)
		fn(req, tx)
	}
}

// rollbackStack undoes a failed Start.
//
// Teardown happens in dependency order: closing the listeners makes the serve
// goroutines return, only then may the server and UA go away, and in-flight handlers
// must finish before the stack is released. Tearing the stack down first would leave
// a goroutine reading a nil server.
func (i *sipIngress) rollbackStack(ctx context.Context) {
	udpConn, tcpLn := i.takeListeners()
	closeListenerPair(i.logger, udpConn, tcpLn)
	if err := waitFor(ctx, &i.serveWG); err != nil {
		i.logger.Warn("timed out waiting for gb28181 sip listeners to stop", zap.Error(err))
	} else if err := i.drainHandlers(ctx); err != nil {
		i.logger.Warn("timed out draining gb28181 sip handlers", zap.Error(err))
	}
	i.closeStack()
}

// closeStack releases the SIP server and user agent. Callers own the ordering: both
// rollbackStack and Stop close the listeners and wait before calling this.
func (i *sipIngress) closeStack() {
	closeQuietly(i.logger, i.client, "sip client")
	closeQuietly(i.logger, i.server, "sip server")
	closeQuietly(i.logger, i.ua, "sip user agent")
	i.ua, i.server, i.client = nil, nil, nil
}

// startSweeper expires registrations that stopped renewing, off the request path.
func (i *sipIngress) startSweeper() {
	sweepCtx, cancelSweep := context.WithCancel(context.Background())
	i.sweepOff = cancelSweep
	i.sweepWG.Add(1)
	go func() {
		defer i.sweepWG.Done()
		i.sweepDevices(sweepCtx)
	}()
}

// bindListeners binds every requested transport, rolling back the ones already bound
// if a later bind fails. A half-bound ingress would look running while devices on one
// transport never reach it.
func (i *sipIngress) bindListeners(cfg Config) error {
	network := strings.ToLower(cfg.SIPTransport)
	// Captured locally: a failed bind clears i.server on the error path while these
	// goroutines may still be starting.
	server := i.server

	if network == TransportUDP || network == TransportBoth {
		conn, err := net.ListenPacket("udp", cfg.SIPListen)
		if err != nil {
			return fmt.Errorf("bind sip udp listener on %s: %w", cfg.SIPListen, err)
		}
		i.udpConn = conn
		i.serveTransport(TransportUDP, func() error { return server.ServeUDP(conn) })
	}
	if network == TransportTCP || network == TransportBoth {
		listener, err := net.Listen("tcp", cfg.SIPListen)
		if err != nil {
			return fmt.Errorf("bind sip tcp listener on %s: %w", cfg.SIPListen, err)
		}
		guarded := newIngressListener(listener, ingressListenerOptions{
			MaxConnections:  i.limits.MaxSignallingConns,
			MaxPerSource:    i.limits.MaxConnsPerSource,
			MaxMessageBytes: i.limits.MaxSIPMessageBytes,
			IdleTimeout:     i.limits.ConnIdleTimeout,
			Logger:          i.logger,
		})
		i.tcpLn = guarded
		i.serveTransport(TransportTCP, func() error { return server.ServeTCP(guarded) })
	}
	return nil
}

// serveTransport runs one transport's accept loop in the background. Everything that
// is a real error is reported by Start; a listener that dies afterwards only updates
// the runtime state (see serveFailed).
func (i *sipIngress) serveTransport(transport string, serve func() error) {
	i.serveWG.Add(1)
	go func() {
		defer i.serveWG.Done()
		if err := serve(); err != nil && !isClosedError(err) {
			i.serveFailed(transport, err)
		}
	}()
}

// serveFailed records a listener that died after the ingress was running. The web
// console keeps serving so an operator can see the state and restart the process.
func (i *sipIngress) serveFailed(transport string, err error) {
	i.logger.Error("gb28181 sip listener stopped",
		zap.String("transport", transport), zap.Error(err))
	if i.runtime != nil {
		i.runtime.SetState(RuntimeStateError, DetailIngressServeFailed)
	}
}

// Stop releases the listeners and waits for the serving goroutines and in-flight
// handlers to finish. The SIP stack is released last, once nothing can be reading it.
//
// It is safe after a partially failed Start and on a never-started ingress.
func (i *sipIngress) Stop(ctx context.Context) error {
	i.mu.Lock()
	started := i.started
	sweepOff := i.sweepOff
	i.started = false
	i.sweepOff = nil
	i.mu.Unlock()
	if !started {
		return nil
	}

	if sweepOff != nil {
		sweepOff()
	}
	i.closeListeners()

	waitErr := waitFor(ctx, &i.sweepWG, &i.serveWG)
	if waitErr == nil {
		waitErr = i.drainHandlers(ctx)
	}
	// The stack is released even when the drain timed out: leaving sockets bound
	// would be worse than a handler that outlives its deadline.
	i.closeStack()

	if waitErr != nil {
		return fmt.Errorf("stop gb28181 ingress: %w", waitErr)
	}
	i.logger.Info("gb28181 sip ingress stopped")
	return nil
}

// drainHandlers waits for the request handlers sipgo is running right now. They are
// not part of serveWG, so this is the only way to know the database is not being
// written after Stop returns.
func (i *sipIngress) drainHandlers(ctx context.Context) error {
	if i.inflight.Load() == 0 {
		return nil
	}
	ticker := time.NewTicker(handlerDrainPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("drain in-flight handlers: %w", ctx.Err())
		case <-ticker.C:
			if i.inflight.Load() == 0 {
				return nil
			}
		}
	}
}

// closeListeners releases the bound listeners. It is the Stop-side path: it takes
// i.mu itself, so callers must not hold it.
func (i *sipIngress) closeListeners() {
	i.mu.Lock()
	udpConn, tcpLn := i.udpConn, i.tcpLn
	i.udpConn, i.tcpLn = nil, nil
	i.mu.Unlock()

	closeListenerPair(i.logger, udpConn, tcpLn)
}

// takeListeners detaches the bound listeners. Callers must hold i.mu, which is why
// the Start error path uses it directly instead of going through closeListeners.
func (i *sipIngress) takeListeners() (net.PacketConn, *ingressListener) {
	udpConn, tcpLn := i.udpConn, i.tcpLn
	i.udpConn, i.tcpLn = nil, nil
	return udpConn, tcpLn
}

func closeListenerPair(logger *zap.Logger, udpConn net.PacketConn, tcpLn *ingressListener) {
	if udpConn != nil {
		closeQuietly(logger, udpConn, "sip udp listener")
	}
	if tcpLn != nil {
		closeQuietly(logger, tcpLn, "sip tcp listener")
	}
}

// readFilter caps UDP datagrams. TCP is capped in framedConn because the transport
// hands the parser arbitrary read chunks, where a per-chunk check proves nothing.
//
// An oversized datagram is dropped by returning no bytes, never by returning an
// error: sipgo treats a filter error as a transport failure and stops the whole UDP
// read loop, so one 20 KiB packet from anyone would otherwise mute SIP for every
// device until restart.
func (i *sipIngress) readFilter(info sip.TransportReadProps, data []byte) ([]byte, error) {
	if !strings.EqualFold(info.Transport, "UDP") || len(data) <= i.limits.MaxSIPMessageBytes {
		return data, nil
	}
	i.oversizedDrops.Add(1)
	source := ""
	if info.RemoteAddr != nil {
		source = hostOnly(info.RemoteAddr.String())
	}
	if log, suppressed := i.throttle.allow("oversized:" + source); log {
		i.logger.Warn("dropped oversized gb28181 sip datagram",
			zap.Int("bytes", len(data)),
			zap.Int("limit", i.limits.MaxSIPMessageBytes),
			zap.String("source", source),
			zap.Int("suppressed", suppressed),
		)
	}
	return nil, nil
}

// sweepDevices expires registrations that stopped renewing.
func (i *sipIngress) sweepDevices(ctx context.Context) {
	ticker := time.NewTicker(i.devices.Interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := i.devices.Sweep(context.Background()); err != nil {
				i.logger.Warn("failed to sweep gb28181 registrations", zap.Error(err))
			}
		}
	}
}

// waitFor waits for every group, or for ctx to expire.
func waitFor(ctx context.Context, groups ...*sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		for _, group := range groups {
			group.Wait()
		}
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isClosedError reports whether an error is the expected result of closing a
// listener, which must not be reported as a failure.
func isClosedError(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled)
}

// closeQuietly releases a resource whose teardown must not mask the error that
// triggered it. Close failures are logged at debug: they are never actionable during
// rollback, but swallowing them would hide a leaking descriptor from the field.
func closeQuietly(logger *zap.Logger, closer io.Closer, name string) {
	if closer == nil {
		return
	}
	err := closer.Close()
	if err == nil || isClosedError(err) {
		return
	}
	if logger != nil {
		logger.Debug("failed to close sip resource", zap.String("resource", name), zap.Error(err))
	}
}

// SendMessage dispatches an outbound SIP MESSAGE request to a device.
func (i *sipIngress) SendMessage(ctx context.Context, device Device, body []byte) error {
	if device.RemoteAddr == nil || *device.RemoteAddr == "" {
		// No recorded source address means this device has not completed a REGISTER in
		// this process; there is nothing reachable to address.
		return apperr.Invalid(CodeDeviceOffline, "device has no recorded remote address", nil)
	}

	i.mu.Lock()
	client := i.client
	ua := i.ua
	outbound := i.outbound
	realm := i.cfg.SIPRealm
	serverID := i.cfg.SIPID
	i.mu.Unlock()

	if client == nil || ua == nil {
		return apperr.Internal(CodeRuntimeNotReady, "sip client is not ready", nil)
	}

	dest := *device.RemoteAddr
	transport := strings.ToUpper(device.Transport)
	if transport == "" {
		transport = upperUDP
	}
	if transport != upperUDP {
		// A stream transport is only reachable through the connection the device
		// registered on: sipgo keys its connection pool by remote address, so a live
		// flow means the request reuses it. A closed flow means the persisted
		// remote_addr is a dead ephemeral port — dialing it would look like a silent
		// failure instead of the truth ("the device must register again").
		if _, err := ua.TransportLayer().GetConnection(strings.ToLower(transport), dest); err != nil {
			return apperr.Precondition(CodeDeviceFlowClosed, "device signalling connection is closed", err)
		}
	}

	recipient, err := recipientURI(device)
	if err != nil {
		return err
	}

	// Consumed only for requests that would actually reach the wire: a closed flow or
	// a malformed address above must not burn the device's budget.
	if outbound != nil && !outbound.allow(device.DeviceID) {
		return apperr.New(apperr.KindRateLimited, CodeOutboundRateLimited,
			"too many platform requests to this device; retry later", nil)
	}

	tagBytes := make([]byte, sipTagBytes)
	if _, err := rand.Read(tagBytes); err != nil {
		// Tag entropy is a protocol requirement (From tag correlation), so a broken
		// entropy source is an error, not something to paper over with a zero tag.
		return fmt.Errorf("generate sip message tag: %w", err)
	}

	req := buildMessageRequest(device, recipient, realm, serverID, tagBytes, body)
	req.SetDestination(dest)
	req.SetTransport(transport)

	res, err := client.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("send sip message to %s: %w", dest, err)
	}
	switch {
	case res.StatusCode == sip.StatusOK:
		return nil
	case res.StatusCode == sip.StatusUnauthorized || res.StatusCode == sip.StatusProxyAuthRequired:
		// P0 does not authenticate outbound requests. Reporting this as a generic
		// send failure would send the operator looking at the network instead of at
		// the device's platform-credential requirement.
		return apperr.Precondition(CodeDeviceChallenged,
			fmt.Sprintf("device challenged the platform request with status %d", res.StatusCode), nil)
	default:
		return fmt.Errorf("sip message rejected with status %d: %s", res.StatusCode, res.Reason)
	}
}

// recipientURI builds the Request-URI for an outbound request: the address the device
// is actually reachable at.
//
// The To/From headers keep the realm-based identity (that is what devices match
// against), while the Request-URI must be routable. Using the realm — usually the
// platform's own SIP ID — as the host produces `sip:<device>@<platform-id>`, which is
// not an address any peer can resolve.
func recipientURI(device Device) (sip.Uri, error) {
	if device.RemoteAddr == nil || *device.RemoteAddr == "" {
		return sip.Uri{}, apperr.Invalid(CodeDeviceOffline, "device has no recorded remote address", nil)
	}
	host, portText, err := net.SplitHostPort(*device.RemoteAddr)
	if err != nil {
		return sip.Uri{}, apperr.Precondition(CodeDeviceOffline,
			"device remote address is not a host:port pair", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || portOutOfRange(port) {
		return sip.Uri{}, apperr.Precondition(CodeDeviceOffline,
			"device remote address has an invalid port", err)
	}
	return sip.Uri{User: device.DeviceID, Host: host, Port: port}, nil
}

// buildMessageRequest assembles an outbound MESSAGE with the identity, correlation and
// content-type headers the MANSCDP body expects. It touches no socket, so the calling
// path only has to decide *where* to send.
//
// Field provenance: the request line comes from the device's live address, while
// From/To carry the realm-based identities GB28181 devices match on. Both forms are
// unverified against a real device — see the package documentation.
func buildMessageRequest(
	device Device,
	recipient sip.Uri,
	realm string,
	serverID string,
	tagBytes []byte,
	body []byte,
) *sip.Request {
	req := sip.NewRequest(sip.MESSAGE, recipient)
	req.AppendHeader(&sip.FromHeader{
		Address: sip.Uri{
			User: serverID,
			Host: realm,
		},
		Params: sip.HeaderParams{{K: "tag", V: fmt.Sprintf("%x", tagBytes)}},
	})
	req.AppendHeader(&sip.ToHeader{
		Address: sip.Uri{
			User: device.DeviceID,
			Host: realm,
		},
	})
	callID := sip.CallIDHeader(fmt.Sprintf("%s-%d", device.DeviceID, time.Now().UnixNano()))
	req.AppendHeader(&callID)
	req.AppendHeader(&sip.CSeqHeader{SeqNo: 1, MethodName: sip.MESSAGE})
	req.AppendHeader(sip.NewHeader("Content-Type", "Application/MANSCDP+xml"))
	req.SetBody(body)
	return req
}

// Sync initiates catalog discovery for an online device.
func (i *sipIngress) Sync(ctx context.Context, device Device) ([]Channel, error) {
	if i.catalogs == nil {
		return nil, apperr.Internal(CodeRuntimeNotReady, "catalog manager is not initialized", nil)
	}
	return i.catalogs.Sync(ctx, device)
}
