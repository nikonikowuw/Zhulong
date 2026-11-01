package gb28181

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

// handleRegister implements the REGISTER UAS with Digest authentication.
//
// Order matters: the allowlist is consulted before the challenge, so an unknown
// device learns nothing about our realm/nonce support beyond a rejection, and a known
// device without a password gets a stable error instead of a challenge it can never
// satisfy. The Digest secret itself is only materialized once a request carries
// credentials, so an unauthenticated REGISTER never costs a decrypt.
func (i *sipIngress) handleRegister(req *sip.Request, tx sip.ServerTransaction) {
	source := hostOnly(req.Source())
	if !i.limiter.allow(source) {
		i.reject(req, tx, sip.StatusServiceUnavailable, "gb28181 register rate limited",
			zap.String("source", source))
		return
	}

	deviceID := fromUser(req.From())
	if err := validateDeviceID(deviceID); err != nil {
		i.respond(req, tx, sip.StatusBadRequest)
		return
	}

	expires, requested, err := parseRegistrationExpires(req)
	if err != nil {
		i.respond(req, tx, sip.StatusBadRequest)
		return
	}

	device, ok := i.authorizeRegister(req, tx, deviceID, source)
	if !ok {
		return
	}
	i.applyRegistration(req, tx, deviceID, registrationLease(expires, requested, device))
}

// authorizeRegister runs the Digest handshake. It reports whether the request
// authenticated, and on success also returns the provisioned device so the caller can
// use its stored lease as the registration default. In every other case the response
// has already been sent.
func (i *sipIngress) authorizeRegister(
	req *sip.Request,
	tx sip.ServerTransaction,
	deviceID, source string,
) (*Device, bool) {
	ctx := context.Background()
	header := req.GetHeader("Authorization")
	if header == nil {
		// Existence only: the challenge must not disclose whether a password exists,
		// and decrypting one for a device that has not authenticated yet is wasted work.
		if _, err := i.store.GetDevice(ctx, deviceID); err != nil {
			i.rejectStoreLookup(req, tx, deviceID, err)
			return nil, false
		}
		i.challenge(req, tx, false)
		return nil, false
	}

	credentials, parseErr := parseDigestCredentials(header.Value())
	if parseErr != nil {
		i.reject(req, tx, sip.StatusBadRequest, "gb28181 register with malformed authorization",
			zap.String("device_id", deviceID), zap.String("source", source))
		return nil, false
	}
	if credentials.Username != deviceID {
		// Throttled like every other refusal: a mismatched username is attacker
		// controlled and must not multiply the log volume.
		if log, suppressed := i.throttle.allow("register username mismatch|" + source); log {
			i.logger.Warn("gb28181 register with mismatched authorization username",
				zap.String("device_id", deviceID),
				zap.String("username", credentials.Username),
				zap.String("source", source),
				zap.Int("suppressed", suppressed),
			)
		}
		i.challenge(req, tx, false)
		return nil, false
	}

	// One row read that also settles existence, now that a credential is on the wire.
	device, password, err := i.store.deviceForAuthentication(ctx, deviceID)
	if err != nil {
		i.rejectStoreLookup(req, tx, deviceID, err)
		return nil, false
	}
	if verifyErr := i.verifyCredentials(credentials, deviceID, req.Recipient.String(), password); verifyErr != nil {
		stale := errors.Is(verifyErr, ErrDigestNonceStale)
		// One 401 per attempt, never a second response on the same transaction: the
		// challenge below *is* the answer.
		if log, suppressed := i.throttle.allow("register auth failed|" + source); log {
			i.logger.Warn("gb28181 register authentication failed",
				zap.String("device_id", deviceID),
				zap.String("source", source),
				zap.Bool("stale_nonce", stale),
				zap.Error(verifyErr),
				zap.Int("suppressed", suppressed),
			)
		}
		i.challenge(req, tx, stale)
		return nil, false
	}
	return device, true
}

// applyRegistration persists the accepted lease, or clears it for EXPIRES=0.
func (i *sipIngress) applyRegistration(req *sip.Request, tx sip.ServerTransaction, deviceID string, expires int) {
	ctx := context.Background()
	if expires == 0 {
		if err := i.devices.Unregistered(ctx, deviceID); err != nil {
			i.logger.Error("failed to clear gb28181 registration",
				zap.String("device_id", deviceID), zap.Error(err))
			i.respond(req, tx, sip.StatusInternalServerError)
			return
		}
		i.respondWithExpiry(req, tx, 0)
		return
	}

	registration := Registration{
		DeviceID:       deviceID,
		Transport:      strings.ToLower(req.Transport()),
		RemoteAddr:     req.Source(),
		ExpiresSeconds: expires,
		RegisteredAt:   time.Now().UTC(),
	}
	if err := i.devices.Registered(ctx, registration); err != nil {
		if isNotFound(err) {
			// The allowlist check and the write cannot disagree unless the row was
			// deleted in between; answer as if the device were unknown.
			i.respond(req, tx, sip.StatusForbidden)
			return
		}
		i.logger.Error("failed to persist gb28181 registration",
			zap.String("device_id", deviceID), zap.Error(err))
		i.respond(req, tx, sip.StatusInternalServerError)
		return
	}
	i.respondWithExpiry(req, tx, expires)
}

// handleMessage acknowledges inbound MESSAGE bodies. Only keepalive changes state in
// S1; catalog and alarm handling belong to later phases, but they must still be
// answered or devices retry them forever.
func (i *sipIngress) handleMessage(req *sip.Request, tx sip.ServerTransaction) {
	source := hostOnly(req.Source())
	if !i.limiter.allow(source) {
		i.reject(req, tx, sip.StatusServiceUnavailable, "gb28181 message rate limited",
			zap.String("source", source))
		return
	}

	deviceID := fromUser(req.From())
	if err := validateDeviceID(deviceID); err != nil {
		i.respond(req, tx, sip.StatusBadRequest)
		return
	}
	if !i.messageSenderAllowed(req, tx, deviceID, source) {
		return
	}
	message, ok := i.messageContent(req, tx, deviceID, source)
	if !ok {
		return
	}

	ctx := context.Background()
	switch strings.ToLower(message.CmdType) {
	case "keepalive":
		if err := i.devices.Keepalive(ctx, deviceID); err != nil {
			i.logger.Warn("failed to record gb28181 keepalive",
				zap.String("device_id", deviceID), zap.Error(err))
			i.respond(req, tx, sip.StatusInternalServerError)
			return
		}
	case "catalog":
		i.handleCatalogMessage(ctx, deviceID, req.Body())
	default:
		// Alarm/DeviceInfo are acknowledged now and processed in later phases.
		i.logger.Debug("gb28181 message acknowledged without a handler",
			zap.String("device_id", deviceID),
			zap.String("cmd_type", message.CmdType),
			zap.Int("sn", message.SN))
	}
	i.respond(req, tx, sip.StatusOK)
}

// handleCatalogMessage feeds one catalog fragment to the manager. Failures are logged
// and swallowed: the device already holds its 200 OK, and a fragment we cannot use
// must not turn into a retransmission storm.
func (i *sipIngress) handleCatalogMessage(ctx context.Context, deviceID string, body []byte) {
	if i.catalogs == nil {
		return
	}
	catResp, err := decodeCatalogResponse(body)
	if err != nil {
		i.logger.Warn("failed to decode gb28181 catalog response",
			zap.String("device_id", deviceID), zap.Error(err))
		return
	}
	if err := i.catalogs.HandleResponse(ctx, deviceID, catResp); err != nil {
		i.logger.Warn("failed to handle gb28181 catalog response",
			zap.String("device_id", deviceID), zap.Error(err))
	}
}

// messageSenderAllowed enforces two rules: the device must be on the allowlist, and a
// registered device is pinned to the source it registered from. Without the pinning, a
// spoofed datagram could keep a dead device "online" by replaying keepalives.
func (i *sipIngress) messageSenderAllowed(req *sip.Request, tx sip.ServerTransaction, deviceID, source string) bool {
	device, err := i.store.GetDevice(context.Background(), deviceID)
	if err != nil {
		if isNotFound(err) {
			i.reject(req, tx, sip.StatusForbidden, "gb28181 message from an unknown device",
				zap.String("device_id", deviceID), zap.String("source", source))
			return false
		}
		i.reject(req, tx, sip.StatusInternalServerError, "gb28181 message from unverifiable device",
			zap.String("device_id", deviceID), zap.Error(err))
		return false
	}
	// Ports are not compared: NAT rebinding changes them, addresses do not.
	if device.RemoteAddr == nil {
		return true
	}
	if hostOnly(*device.RemoteAddr) == source {
		return true
	}
	i.reject(req, tx, sip.StatusForbidden, "gb28181 message from an unexpected source",
		zap.String("device_id", deviceID),
		zap.String("registered_source", hostOnly(*device.RemoteAddr)),
		zap.String("source", source))
	return false
}

// messageContent sizes and decodes the XML body, answering the request itself when
// the body is unusable.
func (i *sipIngress) messageContent(
	req *sip.Request,
	tx sip.ServerTransaction,
	deviceID string,
	source string,
) (gbXMLMessage, bool) {
	body := req.Body()
	if len(body) > i.limits.MaxXMLBytes {
		i.reject(req, tx, sip.StatusRequestEntityTooLarge,
			"gb28181 message body exceeds the size limit",
			zap.String("device_id", deviceID), zap.Int("bytes", len(body)))
		return gbXMLMessage{}, false
	}
	message, err := decodeXML(body)
	if err != nil {
		i.reject(req, tx, sip.StatusBadRequest, "gb28181 message with an unusable body",
			zap.String("device_id", deviceID),
			zap.String("source", source),
			zap.Error(err))
		return gbXMLMessage{}, false
	}
	return message, true
}

// handleOptions answers a liveness probe. It is rate limited like every other inbound
// request but not checked against the allowlist: a device probes OPTIONS before it has
// ever registered, and answering a probe is what RFC 3261 §11.2 asks of any UAS.
func (i *sipIngress) handleOptions(req *sip.Request, tx sip.ServerTransaction) {
	source := hostOnly(req.Source())
	if !i.limiter.allow(source) {
		i.reject(req, tx, sip.StatusServiceUnavailable, "gb28181 options rate limited",
			zap.String("source", source))
		return
	}
	i.respond(req, tx, sip.StatusOK)
}

// handleUnsupported answers methods this UAS does not implement in P0. Answering is
// mandatory: silence makes well-behaved devices retransmit until they give up.
func (i *sipIngress) handleUnsupported(req *sip.Request, tx sip.ServerTransaction) {
	i.logger.Debug("gb28181 unsupported sip method",
		zap.String("method", string(req.Method)), zap.String("source", req.Source()))
	i.respond(req, tx, sip.StatusNotImplemented)
}

// verifyCredentials checks the nonce and the digest response. requestURI is the
// Request-URI the device authenticated against, which the digest must not contradict.
func (i *sipIngress) verifyCredentials(
	credentials digestCredentials,
	deviceID string,
	requestURI string,
	password string,
) error {
	if err := i.nonces.Verify(credentials.Nonce); err != nil {
		return err
	}
	if credentials.Realm != i.cfg.SIPRealm {
		return ErrDigestRealmMismatch
	}
	if credentials.Username != deviceID {
		return ErrDigestUserMismatch
	}
	return verifyDigestResponse(credentials, string(sip.REGISTER), requestURI, i.cfg.SIPRealm, password)
}

// rejectStoreLookup maps a failed allowlist lookup onto a SIP status. An unknown
// device gets 403: the allowlist is an operator decision, not a device error.
func (i *sipIngress) rejectStoreLookup(req *sip.Request, tx sip.ServerTransaction, deviceID string, err error) {
	if isNotFound(err) {
		i.reject(req, tx, sip.StatusForbidden, "gb28181 request from a device that is not provisioned",
			zap.String("device_id", deviceID),
			zap.String("source", req.Source()))
		return
	}
	i.reject(req, tx, sip.StatusInternalServerError, "failed to read gb28181 device credential",
		zap.String("device_id", deviceID), zap.Error(err))
}

// parseRegistrationExpires resolves the lease the device asked for. It reports whether
// the device actually asked (a provided lease wins over the provisioned default) and
// clamps explicit values to a sane range; 0 means "unregister" and is passed through.
//
// Both the Expires header and the Contact parameter are used in the field, so both are
// read. A malformed value is a device error, not something to silently repair.
func parseRegistrationExpires(req *sip.Request) (expires int, requested bool, err error) {
	var raw string
	if header := req.GetHeader("Expires"); header != nil {
		raw = strings.TrimSpace(header.Value())
	}
	if raw == "" {
		if contact := req.Contact(); contact != nil {
			if value, ok := contact.Params.Get("expires"); ok {
				raw = strings.TrimSpace(value)
			}
		}
	}
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, false, fmt.Errorf("invalid expires %q", raw)
	}
	if value == 0 {
		return 0, true, nil
	}
	return clampRegistrationSeconds(value), true, nil
}

// registrationLease resolves the lease to grant: what the device asked for when it
// asked, otherwise the operator's pre-provisioned value, otherwise the default.
//
// The provisioned value is a default, not a ceiling: a device owns its own renewal
// rhythm, and the granted lease is what the sweeper enforces.
func registrationLease(requested int, provided bool, device *Device) int {
	if provided {
		return requested
	}
	if device != nil && device.ExpiresSeconds > 0 {
		return clampRegistrationSeconds(device.ExpiresSeconds)
	}
	return DefaultExpiresSeconds
}

// clampRegistrationSeconds keeps a lease inside the range the sweeper and the API both
// assume.
func clampRegistrationSeconds(value int) int {
	if value < minRegistrationSeconds {
		return minRegistrationSeconds
	}
	if value > maxRegistrationSeconds {
		return maxRegistrationSeconds
	}
	return value
}

// fromUser extracts the device identity from a From header.
func fromUser(header *sip.FromHeader) string {
	if header == nil {
		return ""
	}
	return strings.TrimSpace(header.Address.User)
}

// hostOnly strips the port from a SIP source address for rate-limit keying.
func hostOnly(source string) string {
	host, _, err := net.SplitHostPort(source)
	if err != nil {
		return source
	}
	return host
}

// isNotFound reports whether err is the domain's not-found error.
func isNotFound(err error) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Code == CodeDeviceNotFound
}
