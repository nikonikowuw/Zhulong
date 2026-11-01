package gb28181

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/emiago/sipgo/sip"
	"go.uber.org/zap"
)

// challenge answers 401 with a fresh nonce. stale=true tells a device whose nonce
// expired to retry immediately instead of treating the password as wrong.
func (i *sipIngress) challenge(req *sip.Request, tx sip.ServerTransaction, stale bool) {
	nonce := i.nonces.Issue()
	value := fmt.Sprintf(`Digest realm="%s", nonce="%s", algorithm=MD5, qop="auth"`, i.cfg.SIPRealm, nonce)
	if stale {
		value += ", stale=true"
	}
	res := sip.NewResponseFromRequest(req, sip.StatusUnauthorized, "Unauthorized", nil)
	res.AppendHeader(sip.NewHeader("WWW-Authenticate", value))
	i.writeResponse(res, tx)
}

// respondWithExpiry answers a successful REGISTER, echoing the granted lease.
func (i *sipIngress) respondWithExpiry(req *sip.Request, tx sip.ServerTransaction, expires int) {
	res := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil)
	if contact := req.Contact(); contact != nil {
		res.AppendHeader(sip.NewHeader("Contact", contact.String()))
	}
	res.AppendHeader(sip.NewHeader("Expires", strconv.Itoa(expires)))
	i.writeResponse(res, tx)
}

// respond answers a request with an empty body. It is silent by design: a refusal
// without forensic value (a malformed device identity, an unparsable Expires) does
// not deserve a log line, so anything worth logging goes through reject instead.
func (i *sipIngress) respond(req *sip.Request, tx sip.ServerTransaction, status int) {
	res := sip.NewResponseFromRequest(req, status, reasonPhrase(status), nil)
	i.writeResponse(res, tx)
}

// reject logs one security- or protocol-relevant refusal and answers it.
//
// The answer is sent every time; only the log line is sampled (one per source per
// message per window, see logThrottle). A rejected flood is exactly when the log
// must not reproduce the flood, and the counters in IngressStats stay exact.
//
// message is required; fields is variadic so the fixed argument count stays at four.
func (i *sipIngress) reject(
	req *sip.Request,
	tx sip.ServerTransaction,
	status int,
	message string,
	fields ...zap.Field,
) {
	source := hostOnly(req.Source())
	if log, suppressed := i.throttle.allow(message + "|" + source); log {
		if suppressed > 0 {
			fields = append(fields, zap.Int("suppressed", suppressed))
		}
		i.logger.Warn(message, fields...)
	}
	i.respond(req, tx, status)
}

// writeResponse adds the Date header, then sends.
//
// The To tag is not added here: sip.NewResponseFromRequest already tags every
// response other than 100 Trying (RFC 3261 §8.2.6.2), which is the only case that
// must stay untagged.
func (i *sipIngress) writeResponse(res *sip.Response, tx sip.ServerTransaction) {
	res.AppendHeader(sip.NewHeader("Date", time.Now().UTC().Format(http.TimeFormat)))
	if err := tx.Respond(res); err != nil {
		i.logger.Debug("failed to send gb28181 sip response",
			zap.Int("status", int(res.StatusCode)), zap.Error(err))
	}
}

// reasonPhrase maps the statuses this UAS sends onto their canonical reason text.
func reasonPhrase(status int) string {
	switch status {
	case sip.StatusOK:
		return "OK"
	case sip.StatusBadRequest:
		return "Bad Request"
	case sip.StatusUnauthorized:
		return "Unauthorized"
	case sip.StatusForbidden:
		return "Forbidden"
	case sip.StatusRequestEntityTooLarge:
		return "Request Entity Too Large"
	case sip.StatusInternalServerError:
		return "Server Internal Error"
	case sip.StatusNotImplemented:
		return "Not Implemented"
	case sip.StatusServiceUnavailable:
		return "Service Unavailable"
	default:
		return "Response"
	}
}
