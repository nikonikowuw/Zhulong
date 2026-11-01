// Package gb28181 implements the GB/T 28181 signalling surface: device registration
// (REGISTER with Digest authentication), keepalive/liveness, catalog discovery and the
// translation of catalog channels into cameras.
//
// # Layering
//
//   - Store: SQLite persistence (config singleton, device allowlist, channel catalog).
//   - sipIngress: the SIP UAS/UAC. It parses and answers signalling, and nothing here
//     touches media bytes or the native engine.
//   - CatalogManager: outbound Catalog queries, SN correlation, fragment reassembly.
//   - Service: the rules shared by HTTP and the lifecycle (normalize → validate →
//     resolve → persist). Saving never reconfigures a running listener.
//   - LifecycleManager: boot-time validation, address resolution, and the one place
//     stale registrations are cleared.
//   - Handler: the protected REST endpoints.
//
// # Protection scope
//
// The ingress bounds what one peer can do: message size, XML size/depth/entities,
// per-source request rate, concurrent and per-source signalling connections, an idle
// deadline on stream transports, and a per-device outbound rate. The rate limiter keys
// on the source address and the source can be spoofed over UDP, so this is protection
// against a stuck or hostile device, not against a distributed flood. The counters in
// IngressStats stay exact; only the log lines are sampled.
//
// # Unverified assumptions
//
// The following are implemented from the standard's shape and from common field
// behaviour, but have not been checked against a vendor device or the standard text.
// They are recorded here so an interop failure is not mistaken for a bug in the layers
// around them, and so nobody upgrades one of them into a "verified" claim by accident:
//
//   - Outbound MESSAGE addressing: the request line carries the device's live address
//     while From/To carry the realm-based identities. Devices that route on the
//     Request-URI's host rather than on To must be checked first.
//   - Outbound authentication: a device that answers a platform MESSAGE with 401/407 is
//     reported as GB28181_DEVICE_CHALLENGED. P0 does not authenticate outbound requests.
//   - Digest binding: the digest-uri is only compared by user part (see
//     verifyDigestURI). Exact Request-URI comparison would be stricter and is what the
//     RFC asks for, but devices in the field rewrite it.
//   - Nonce policy: a nonce is HMAC-signed and time-bounded, not single-use, because UDP
//     REGISTER retransmissions replay the same Authorization byte for byte.
//   - Catalog completion: an exchange is complete on SumNum satisfied, SumNum == 0, or an
//     empty first fragment. A device that reports SumNum late relies on the timeout.
package gb28181
