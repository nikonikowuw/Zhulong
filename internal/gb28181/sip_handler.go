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

	expires, err := registrationExpires(req)
	if err != nil {
		i.respond(req, tx, sip.StatusBadRequest)
		return
	}

	if !i.authorizeRegister(req, tx, deviceID, source) {
		return
	}
	i.applyRegistration(req, tx, deviceID, expires)
}

// authorizeRegister runs the Digest handshake. It reports whether the request
// authenticated; in every other case the response has already been sent.
func (i *sipIngress) authorizeRegister(req *sip.Request, tx sip.ServerTransaction, deviceID, source string) bool {
	ctx := context.Background()
	header := req.GetHeader("Authorization")
	if header == nil {
		// Existence only: the challenge must not disclose whether a password exists,
		// and decrypting one for a device that has not authenticated yet is wasted work.
		if _, err := i.store.GetDevice(ctx, deviceID); err != nil {
			i.rejectStoreLookup(req, tx, deviceID, err)
			return false
		}
		i.challenge(req, tx, false)
		return false
	}

	credentials, parseErr := parseDigestCredentials(header.Value())
	if parseErr != nil {
		i.reject(req, tx, sip.StatusBadRequest, "gb28181 register with malformed authorization",
			zap.String("device_id", deviceID), zap.String("source", source))
		return false
	}
	if credentials.Username != deviceID {
		i.logger.Warn("gb28181 register with mismatched authorization username",
			zap.String("device_id", deviceID),
			zap.String("username", credentials.Username),
			zap.String("source", source))
		i.challenge(req, tx, false)
		return false
	}

	// One row read that also settles existence, now that a credential is on the wire.
	password, err := i.store.devicePassword(ctx, deviceID)
	if err != nil {
		i.rejectStoreLookup(req, tx, deviceID, err)
		return false
	}
	if verifyErr := i.verifyCredentials(credentials, deviceID, password); verifyErr != nil {
		stale := errors.Is(verifyErr, ErrDigestNonceStale)
		i.logger.Warn("gb28181 register authentication failed",
			zap.String("device_id", deviceID),
			zap.String("source", source),
			zap.Bool("stale_nonce", stale),
			zap.Error(verifyErr))
		i.challenge(req, tx, stale)
		return false
	}
	return true
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
	default:
		// Catalog/Alarm/DeviceInfo are acknowledged now and processed in S2.
		i.logger.Debug("gb28181 message acknowledged without a handler",
			zap.String("device_id", deviceID),
			zap.String("cmd_type", message.CmdType),
			zap.Int("sn", message.SN))
	}
	i.respond(req, tx, sip.StatusOK)
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
	deviceID, source string,
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

func (i *sipIngress) handleOptions(req *sip.Request, tx sip.ServerTransaction) {
	i.respond(req, tx, sip.StatusOK)
}

// handleUnsupported answers methods this UAS does not implement in P0. Answering is
// mandatory: silence makes well-behaved devices retransmit until they give up.
func (i *sipIngress) handleUnsupported(req *sip.Request, tx sip.ServerTransaction) {
	i.logger.Debug("gb28181 unsupported sip method",
		zap.String("method", string(req.Method)), zap.String("source", req.Source()))
	i.respond(req, tx, sip.StatusNotImplemented)
}

// verifyCredentials checks the nonce and the digest response.
func (i *sipIngress) verifyCredentials(credentials digestCredentials, deviceID, password string) error {
	if err := i.nonces.Verify(credentials.Nonce); err != nil {
		return err
	}
	if credentials.Realm != i.cfg.SIPRealm {
		return ErrDigestRealmMismatch
	}
	if credentials.Username != deviceID {
		return ErrDigestUserMismatch
	}
	return verifyDigestResponse(credentials, string(sip.REGISTER), i.cfg.SIPRealm, password)
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

// registrationExpires resolves the requested lease from the Expires header, then the
// Contact parameter (both are used in the field), clamped to a sane range.
func registrationExpires(req *sip.Request) (int, error) {
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
		return DefaultExpiresSeconds, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid expires %q", raw)
	}
	if value == 0 {
		return 0, nil
	}
	if value < minRegistrationSeconds {
		value = minRegistrationSeconds
	}
	if value > maxRegistrationSeconds {
		value = maxRegistrationSeconds
	}
	return value, nil
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
