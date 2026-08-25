# Test fixtures

All fixtures here are invented. They contain no production data, no real
hostnames, and only RFC 5737 documentation IP addresses. `make check` runs
`scripts/check-prohibited-content.sh` over the repository to keep it that way.

- `formats/<format>/` — small samples of each supported log grammar, covering
  line terminators (LF, CRLF, missing final newline), malformed records, and
  format-specific edge cases.
- `incidents/database-timeout/` — a three-file incident used by the parser
  tests: successful traffic, an HTTP failure, and the matching application and
  Catalina timeout evidence.

These were originally emitted by a fixture generator that has since been
removed. They are now plain checked-in files; edit them directly.
