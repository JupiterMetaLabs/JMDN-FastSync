# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |

## Security Model & Deployment Assumptions

JMDN-FastSync is designed for **authenticated peer-to-peer deployments only**. The protocol assumes:

- All participating nodes are authenticated via UUID-based authentication before any sync operations
- The network layer uses libp2p with Noise Protocol encryption
- Communication occurs only between known, trusted blockchain nodes in the Jupiter Meta ecosystem

**This is not a general-purpose public sync protocol.** It is specifically designed for private, permissioned blockchain networks where all peers are authenticated.

## Known Security Considerations

### 1. Unbounded Message Allocation (ReadDelimited)

**Status:** Accepted risk by design

The `ReadDelimited` function does not enforce a maximum message size limit. This is a deliberate design choice because:

- The protocol cannot know the prefix length of payloads in advance (variable-size data)
- Data exchange only occurs between authenticated nodes, not arbitrary peers
- The pre-authentication endpoint has rate limiting to prevent abuse

**Mitigation:** Authentication is required before any data exchange. Rate limiting applies to the availability endpoint.

### 2. Client-Supplied Parameters

Several handlers accept client-provided parameters without strict server-side clamping:

- **Tag batches:** Client splits tags into bounded batches (max 1500 headers / 30 data blocks). Server receives one pre-sized batch at a time.
- **Block ranges in availability:** Pure arithmetic computation (no DB access, no allocation) — no attack surface
- **PoTS blocks map:** By design for quick sync of missed blocks — window is tiny (single/low double digits)

All these are acceptable because:
- Peers are pre-authenticated
- Operations are bounded by design or pure arithmetic
- Memory impact is minimal

### 3. Checksum Versions

The protocol supports both CRC32 and SHA-256 checksums. CRC32 is used as a secondary data-loss check layered over the encrypted transport. SHA-256 is available when needed.

**Rationale:** CRC32 provides lightweight integrity verification for data transmission errors, not cryptographic security. The underlying libp2p Noise Protocol provides transport security.

### 4. Rate Limiting

Rate limiting is applied only to `HandleAvailability` (the pre-authentication endpoint) with burst=3 and 30s refill. Other endpoints require valid auth UUIDs.

**Rationale:** Authenticated endpoints trust the peer identity. Broader throttling is handled at the blockchain node level.

### 5. Development Mode Logging

The `DEVELOPMENT` constant in `common/types/constants/logging.go` controls logging verbosity. In production builds, this should be set to `false` to avoid verbose stack traces and ensure optimal encoder performance.

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly:

**Email:** security@jupitermeta.io

Please include:
- Description of the vulnerability
- Steps to reproduce (if applicable)
- Potential impact assessment
- Any suggested mitigations

We aim to respond to security reports within 48 hours and will work with you to verify and address the issue.

## Disclosure Policy

- We follow a 90-day disclosure policy for verified vulnerabilities
- We will credit researchers who report valid security issues (with their permission)
- We request that you do not publicly disclose vulnerabilities until we have released a fix

## Security Scanning

To check for known vulnerabilities in dependencies:

```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

## Audit History

| Date       | Auditor | Score  | Notes                            |
| ---------- | ------- | ------ | -------------------------------- |
| 2026-03-31 | Claude  | 88/100 | Open-source readiness audit        |

See audit report in project documentation for full details.
