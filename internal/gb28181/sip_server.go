package gb28181

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

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

	// oversizedDrops counts datagrams refused by the size cap so the operator can see
	// that inbound protection is firing in the field.
	oversizedDrops atomic.Uint64
}

// droppedOversized returns how many inbound datagrams were refused for exceeding
// the size cap.
func (i *sipIngress) droppedOversized() uint64 { return i.oversizedDrops.Load() }

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
func (i *sipIngress) Start(ctx context.Context, cfg Config, advertisedIP string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.started {
		return nil
	}
	if i.store == nil {
		return errors.New("gb28181 ingress requires a device store")
	}
	i.cfg = cfg
	i.advertisedIP = advertisedIP

	if err := i.initialize(ctx); err != nil {
		return err
	}
	if err := i.buildStack(); err != nil {
		return err
	}
	if err := i.bindListeners(cfg); err != nil {
		i.rollbackStack()
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
// secret, the inbound limiter, and the device state machine.
func (i *sipIngress) initialize(ctx context.Context) error {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("seed gb28181 nonce secret: %w", err)
	}
	i.nonces = newNonceManager(secret, i.limits.NonceTTL)
	i.limiter = newRateLimiter(i.limits.RateBurst, i.limits.RateRefill)
	i.devices = newDeviceStateMachine(deviceStateOptions{
		Store:             i.store,
		Runtime:           i.runtime,
		Logger:            i.logger,
		KeepaliveTimeout:  i.limits.KeepaliveTimeout,
		RegistrationGrace: i.limits.RegistrationGrace,
		SweepInterval:     i.limits.SweepInterval,
	})

	// A registration persisted by a previous process is not evidence of liveness, so
	// the table is cleared before the first REGISTER can be accepted.
	if _, err := i.devices.Reset(ctx); err != nil {
		return fmt.Errorf("reset gb28181 registrations: %w", err)
	}
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
	server.OnRegister(i.handleRegister)
	server.OnMessage(i.handleMessage)
	server.OnOptions(i.handleOptions)
	server.OnNoRoute(i.handleUnsupported)

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

// rollbackStack undoes a failed Start.
//
// Teardown happens in dependency order: closing the listeners makes the serve
// goroutines return, and only then may the server and UA go away. Tearing them down
// first would leave a goroutine reading a nil server.
func (i *sipIngress) rollbackStack() {
	i.closeListeners()
	i.serveWG.Wait()
	i.closeStack()
}

// closeStack releases the SIP server and user agent. The serve goroutines read
// i.server, so callers own the ordering: rollbackStack waits for them first, Stop
// closes the listeners and then waits.
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
		guarded := newIngressListener(
			listener,
			i.limits.MaxSignallingConns,
			i.limits.MaxSIPMessageBytes,
			i.logger,
		)
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

// Stop releases the listeners and waits for the serving goroutines to finish.
//
// It is safe after a partially failed Start and on a never-started ingress.
func (i *sipIngress) Stop(ctx context.Context) error {
	i.mu.Lock()
	started := i.started
	i.started = false
	i.mu.Unlock()
	if !started {
		return nil
	}

	if i.sweepOff != nil {
		i.sweepOff()
		i.sweepOff = nil
	}
	i.closeListeners()
	i.closeStack()

	done := make(chan struct{})
	go func() {
		i.sweepWG.Wait()
		i.serveWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("stop gb28181 ingress: %w", ctx.Err())
	}

	i.logger.Info("gb28181 sip ingress stopped")
	return nil
}

func (i *sipIngress) closeListeners() {
	if i.udpConn != nil {
		closeQuietly(i.logger, i.udpConn, "sip udp listener")
		i.udpConn = nil
	}
	if i.tcpLn != nil {
		closeQuietly(i.logger, i.tcpLn, "sip tcp listener")
		i.tcpLn = nil
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
	i.logger.Warn("dropped oversized gb28181 sip datagram",
		zap.Int("bytes", len(data)),
		zap.Int("limit", i.limits.MaxSIPMessageBytes),
		zap.String("source", info.RemoteAddr.String()),
	)
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
	realm := i.cfg.SIPRealm
	serverID := i.cfg.SIPID
	i.mu.Unlock()

	if client == nil || ua == nil {
		return apperr.Internal(CodeRuntimeNotReady, "sip client is not ready", nil)
	}

	dest := *device.RemoteAddr
	transport := strings.ToUpper(device.Transport)
	if transport == "" {
		transport = strings.ToUpper(TransportUDP)
	}
	if transport != strings.ToUpper(TransportUDP) {
		// A stream transport is only reachable through the connection the device
		// registered on: sipgo keys its connection pool by remote address, so a live
		// flow means the request reuses it. A closed flow means the persisted
		// remote_addr is a dead ephemeral port — dialing it would look like a silent
		// failure instead of the truth ("the device must register again").
		if _, err := ua.TransportLayer().GetConnection(strings.ToLower(transport), dest); err != nil {
			return apperr.Precondition(CodeDeviceFlowClosed, "device signalling connection is closed", err)
		}
	}

	tagBytes := make([]byte, 8)
	if _, err := rand.Read(tagBytes); err != nil {
		// Tag entropy is a protocol requirement (From tag correlation), so a broken
		// entropy source is an error, not something to paper over with a zero tag.
		return fmt.Errorf("generate sip message tag: %w", err)
	}

	req := sip.NewRequest(sip.MESSAGE, sip.Uri{
		User: device.DeviceID,
		Host: realm,
	})
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
	req.SetDestination(dest)
	req.SetTransport(transport)

	res, err := client.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("send sip message to %s: %w", dest, err)
	}
	if res.StatusCode != sip.StatusOK {
		return fmt.Errorf("sip message rejected with status %d: %s", res.StatusCode, res.Reason)
	}
	return nil
}

// Sync initiates catalog discovery for an online device.
func (i *sipIngress) Sync(ctx context.Context, device Device) ([]Channel, error) {
	if i.catalogs == nil {
		return nil, apperr.Internal(CodeRuntimeNotReady, "catalog manager is not initialized", nil)
	}
	return i.catalogs.Sync(ctx, device)
}
