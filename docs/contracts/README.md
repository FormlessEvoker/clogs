# Behavior contracts (archived, unaudited)

These documents were written to freeze the tool's observable behavior before a
large generalization effort. That effort was abandoned; the work it planned for
was either done differently or dropped.

They are kept because they still describe real behavior in useful detail —
detection precedence, timestamp handling, malformed-line accounting, signature
composition, storage constraints, and analysis semantics — and because that
detail is expensive to reconstruct.

**They have not been audited since the configuration work landed, and at least
one section is known to be wrong:**

- `log-format-contract.md` describes a built-in route-normalization rule that
  rewrites `/svc/v4/api/site/<value>/…` to `/svc/v4/api/site/{site}/…`. No such
  built-in rule exists. Route normalization is now driven entirely by
  `route_templates` in configuration, and there is no default. See
  [configuration.md](../configuration.md).

Each document also opens by framing itself as a baseline for future roadmap
work — a parser registry, a profile-based configuration model, a generalized
schema. None of that was built, and the roadmap describing it has been removed.

Treat anything here as a lead to verify against the code and tests, not as a
statement of current behavior. For current behavior see the tests, which are
the actual contract, and [commands.md](../commands.md).
