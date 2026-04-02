# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Tillr, please report it responsibly.

**Email:** mschulkind@gmail.com

Please include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

## Response Timeline

- **Acknowledgment:** Within 48 hours
- **Initial assessment:** Within 1 week
- **Fix timeline:** Depends on severity, typically within 2 weeks for critical issues

## Scope

Security-relevant areas include:
- **CLI tool security** — command injection, unsafe file operations
- **SQLite injection** — parameterized queries must be used throughout
- **Cross-site scripting (XSS)** — web viewer must not execute arbitrary scripts
- **Authentication** — if authentication is added in the future, it must be secure by default

## Supported Versions

Only the latest version on the `main` branch is supported with security fixes. There are no LTS releases at this time.

## Disclosure Policy

We follow coordinated disclosure. Please allow reasonable time for a fix before public disclosure.
