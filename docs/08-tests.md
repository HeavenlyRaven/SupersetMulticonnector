# 8. The tests

Tests are documentation that cannot go out of date. If you want to know what
a function is *supposed* to do, its tests answer more reliably than its
comments.

## Running them

```bash
go test ./...                              # all Go unit tests, ~seconds
go test -v ./internal/validate/            # one package, verbose
go test -tags integration ./test/integration/...   # the full stack, needs Docker
pytest extension/backend/tests/            # the Python shim's tests
```

Current state of the Go suite:

```
ok      github.com/acme/superset-federation/cmd/fedctl
ok      github.com/acme/superset-federation/internal/config
ok      github.com/acme/superset-federation/internal/ddl
ok      github.com/acme/superset-federation/internal/sources
ok      github.com/acme/superset-federation/internal/validate
?       internal/health, internal/hub, internal/jsonio, internal/preflight   [no test files]
```

The four untested packages are the ones that are mostly I/O — they are
covered indirectly by the integration test.

## How Go tests work

A file ending in `_test.go`, a function starting with `Test`, one parameter:

```go
func TestPort(t *testing.T) {
    if err := Port(0); err == nil {
        t.Errorf("Port(0): expected rejection")
    }
}
```

- `t.Errorf` reports a failure and **keeps going**.
- `t.Fatalf` reports and **stops this test immediately**.
- `t.Helper()` marks a function as a helper, so failures are reported at the
  caller's line, not inside the helper.
- `t.TempDir()` creates a temporary directory that is deleted automatically.
- `t.Setenv(k, v)` sets an environment variable and restores it after.
- `t.Run(name, func)` creates a subtest, so one failing case is named
  precisely.

Test files live in the same package as the code, so they can call unexported
functions — which is why `postgresConnConfig` can be tested directly despite
being lowercase.

---

## [internal/validate/validate_test.go](../internal/validate/validate_test.go)

Seventeen tests. This is the file to read first, because it defines the
security guarantees in executable form.

### `TestSourceName`

```go
invalid := []string{
    "",
    "AB",                         // uppercase
    "1abc",                       // must start with a letter
    "ab",                         // too short
    "foo; DROP DATABASE default", // injection attempt from acceptance criteria
    "foo-bar", "foo.bar", "foo bar", "foo`bar", "foo'bar",
    string(make([]byte, 50)),     // way too long / null bytes
}
```

The SQL injection string is there because the specification's acceptance
criteria named it. Note `string(make([]byte, 50))` — fifty zero bytes, testing
both the length limit and NUL byte handling at once.

The test also asserts the **error code**, not just that an error happened:

```go
if code := errCode(t, err); code != jsonio.CodeInvalidName { ... }
```

That matters because the code determines the HTTP status a browser user sees.
A right-idea-wrong-code error would give a 500 instead of a 400.

### The fake resolver

```go
type fakeResolver struct {
    answers map[string][]net.IPAddr
}

func (f fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
    addrs, ok := f.answers[host]
    if !ok {
        return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
    }
    return addrs, nil
}
```

Twelve lines, and they make SSRF testing possible. The tests can now say
"pretend `metadata.internal` resolves to `169.254.169.254`" without owning a
domain or touching the network. This is the payoff of `validate.Host`
accepting a `Resolver` interface rather than calling DNS directly.

### `TestHost_DeniedRanges`

A table-driven test — one entry per denied range:

```go
{"loopback-v4",  "loop.internal",     "127.0.0.1"},
{"loopback-v6",  "loop6.internal",    "::1"},
{"link-local",   "metadata.internal", "169.254.169.254"},  // cloud metadata endpoint
{"rfc1918-10",   "priv10.internal",   "10.1.2.3"},
{"rfc1918-172",  "priv172.internal",  "172.20.0.5"},
{"rfc1918-192",  "priv192.internal",  "192.168.1.1"},
{"ula-v6",       "ula.internal",      "fd00::1"},
```

Each runs as a named subtest, so a failure says exactly which range broke.
**Table-driven tests are the dominant Go testing style** — adding a case is
one line.

The remaining host tests cover the rest of the contract:

| Test | Guarantee |
|---|---|
| `TestHost_PublicAllowed` | A normal public address is not blocked |
| `TestHost_MultiAddressRejectsIfAnyDenied` | A hostname resolving to both a public and a private IP is rejected |
| `TestHost_AllowlistOverridesByHostname` | An allowlisted hostname bypasses the deny rule |
| `TestHost_AllowlistOverridesByCIDR` | An allowlisted CIDR does too |
| `TestHost_NXDOMAIN` | A hostname that does not resolve is rejected, not allowed |

The multi-address one is the subtle case: it is the difference between
checking `addrs[0]` and checking all of them.

### The SQLite path tests

Five attacks, five tests:

| Test | Attack |
|---|---|
| `TestSQLitePath_Traversal` | `../../etc/passwd` |
| `TestSQLitePath_AbsoluteEscape` | An absolute path to a file outside the directory |
| `TestSQLitePath_MissingFile` | A file that does not exist |
| `TestSQLitePath_NotRegularFile` | A directory instead of a file |
| `TestSQLitePath_EscapingSymlink` | A symlink inside the directory pointing outside it |

The symlink test creates a real symlink and skips on Windows, where creating
one usually needs elevated privileges:

```go
if runtime.GOOS == "windows" {
    t.Skip("symlink creation typically requires elevated privileges on Windows")
}
```

Skipping with a reason is the right move — better than a test that fails for
environmental reasons and gets ignored.

### `TestCredentialFlagName`

```go
yes := []string{
    "--password", "-p", "password", "--db-password", "--secret", "--api-token-secret", "PASSWORD", "--pwd",
    // Gaps found by adversarial review: realistic spellings a developer or
    // user might actually type.
    "--db-pass", "--dbpass", "--dbpwd", "--userpwd", "--auth-token",
    "--authtoken", "--api-key", "--apikey", "--pgpassword", "--mysql-pwd",
    "--client-secret",
}
no := []string{"--host", "--port", "--name", "--database", "--dev", "--path", "--schema", "--timeout", "--file", "-f"}
```

The comment records that the second group was added after deliberately
attacking the check — someone sat down and thought "how would I spell a
password flag such that this misses it?" and added every answer.

The `no` list matters just as much: a check that rejects `--host` would make
the tool unusable. Both directions are asserted.

---

## [internal/ddl/ddl_test.go](../internal/ddl/ddl_test.go)

### Golden-file testing

A "golden" test asserts the output matches an exact expected value:

```go
wantColl := "CREATE NAMED COLLECTION IF NOT EXISTS `fed_fed_sales_creds` AS host = ?, port = ?, user = ?, password = ?, database = ?, schema = ?"
if plan.Steps[0].Do.Text != wantColl {
    t.Errorf("collection DDL mismatch:\n got: %s\nwant: %s", plan.Steps[0].Do.Text, wantColl)
}
```

Character for character. This is appropriate here because the SQL is
security-relevant — an accidental change from `?` to string interpolation
would be caught instantly.

(This is also where you can see the double-prefix quirk in action:
`AttachPostgres("fed_sales", ...)` produces `fed_fed_sales_creds`. The test
documents the behaviour as it actually is.)

### The leak assertion

Every golden test calls this first:

```go
func assertNoSecretLeak(t *testing.T, plan Plan) {
    for _, step := range plan.Steps {
        if strings.Contains(step.Do.Text, secretPassword) {
            t.Errorf("statement text leaks the password: %q", step.Do.Text)
        }
        if step.Undo != nil && strings.Contains(step.Undo.Text, secretPassword) { ... }
        if strings.Contains(step.Do.Text, "?") == false && len(step.Do.Args) > 0 {
            t.Errorf("statement has bind args but no placeholder: %q", step.Do.Text)
        }
    }
}
```

Three assertions:

1. The password does not appear in any statement text.
2. Nor in any undo statement.
3. A statement with bind arguments must contain a placeholder — catching the
   case where someone interpolates values into the text but leaves the args
   attached.

This is a **property test**: rather than checking one specific output, it
checks a rule that must hold for every output. Those tend to survive
refactoring better than exact-match tests.

The rest of the file covers the default schema, name rejection, missing
fields, bad ports, the MySQL and SQLite shapes, both detach variants, and
that `Plan.Statements()` returns the `Do` statements in order for
`fedctl plan`.

---

## [internal/sources/](../internal/sources/) tests

[sources_test.go](../internal/sources/sources_test.go) covers the registry
(all three types registered, unknown types rejected) and each type's
`Validate` — required fields, the `public` schema default, port rejection,
and SQLite path resolution.

[testconn_test.go](../internal/sources/testconn_test.go) contains the single
most interesting test in the repository:
`TestPostgresConnConfig_NoFallbackLeak`. Its comment is a small incident
report describing two consecutive bugs:

> First: [building the config from an empty DSN] makes pgx populate
> libpq-style Fallbacks (127.0.0.1 and ::1) that pgx will still dial on
> connection failure — a genuine SSRF-guard bypass.
>
> Second (the original fix for the first bug, here corrected): clearing every
> fallback unconditionally broke sslmode=prefer's legitimate
> TLS-then-plaintext retry... live-verified against a real Postgres server
> with no TLS configured.

And then it states the invariant that satisfies both:

```go
for _, fb := range cfg.Fallbacks {
    if fb.Host != cfg.Host || fb.Port != cfg.Port {
        t.Errorf("host %q: fallback dials %s:%d, a different address than the validated host %s:%d", ...)
    }
}
```

Not "there are no fallbacks", but "no fallback points anywhere else". The
test encodes the *correct* rule, so neither bug can return — including the
one that was introduced by fixing the other.

That is worth internalising: **the second bug came from an over-broad fix for
the first.** The test that finally pinned it down describes the actual
requirement rather than the symptom.

The two `RefusesFast` tests connect to port 1 (reserved, never listening) and
assert a prompt failure rather than a hang — a check that timeouts are wired
up.

`TestSQLiteType_TestConnection_MissingFile` guards the file-creation
surprise: without the `os.Stat` check, opening a nonexistent path would
create an empty database and report success.

---

## [internal/config/config_test.go](../internal/config/config_test.go)

`TestValidateMetadataURI` asserts both directions:

```go
bad := []string{"", "sqlite:////app/superset_home/superset.db", "SQLite:///tmp/x.db", "mysql://user:pass@host/db"}
good := []string{"postgresql://...", "postgresql+psycopg2://..."}
```

`"SQLite:///tmp/x.db"` with a capital S is there deliberately — checking that
the comparison is case-insensitive. A case-sensitive check would let this
one through, and the whole point is that SQLite must never be accepted.

`TestSourceSpec_ResolveParams` verifies the password comes from the named
environment variable, and `_MissingEnv` verifies a clear error when it is
unset. `TestLoadSourcesFile_RejectsDuplicates` catches a duplicated name in
YAML.

---

## [cmd/fedctl/](../cmd/fedctl/) tests

`TestPrintCLIError_PlainErrorsAreNotMangled` is a regression test with a
long explanatory comment:

```go
// an earlier version routed every printCLIError call through jsonio.AsError,
// whose fallback silently replaces any error that isn't a *jsonio.Error with
// the generic string "internal error" — appropriate for a --json API response
// but useless for a plain CLI error a human operator needs to troubleshoot.
```

It asserts both halves: the real message is present, and the word "internal
error" is *absent*.

```go
if bytes.Contains(captured, []byte("internal error")) {
    t.Errorf("plain error was mangled into a generic message: %q", captured)
}
```

Asserting the absence of the wrong behaviour, not just the presence of the
right one, is what makes it a proper regression test.

To check what was printed, the test swaps `os.Stderr` for a pipe — a standard
Go trick for capturing output.

`dotenv_test.go` covers comments, blank lines, both quote styles, malformed
lines, and the precedence rule:

```go
func TestLoadDotEnv_DoesNotOverrideExplicitlySetVar(t *testing.T) {
    ...
    if v := os.Getenv("ALREADY_SET"); v != "from-shell" {
        t.Errorf("expected an explicitly-set shell variable to win over .env, got %q", v)
    }
}
```

---

## [extension/backend/tests/](../extension/backend/tests/) — the Python tests

### The stub problem

The shim imports Flask, Flask-AppBuilder, and `superset_core`. Installing all
of Superset just to test 200 lines of glue would be absurd. So
[conftest.py](../extension/backend/tests/conftest.py) fabricates them:

```python
def _noop_decorator_factory(*_args, **_kwargs):
    def deco(fn):
        return fn
    return deco

fab_api_mod = types.ModuleType("flask_appbuilder.api")
fab_api_mod.expose = _noop_decorator_factory
fab_api_mod.protect = _noop_decorator_factory
...
sys.modules["flask_appbuilder.api"] = fab_api_mod
```

`sys.modules` is Python's import cache. Inserting a module object there means
a later `import flask_appbuilder.api` finds the fake and never looks on disk.
The decorators become no-ops, and `RestApi` becomes a stub whose `response()`
just returns a dictionary.

`conftest.py` is a pytest convention: it runs automatically before the tests
in its directory.

Is testing against stubs weaker than testing against the real framework? Yes.
But what is being tested — permission gating, subprocess arguments, error
mapping — does not depend on the framework's behaviour. And the alternative,
in practice, is no tests at all.

### The seven tests

| Test | Guarantee |
|---|---|
| `test_add_source_denied_without_can_manage_and_never_execs_fedctl` | 403 **and** `subprocess.run` never called |
| `test_remove_source_denied_without_can_manage` | Same for delete |
| `test_add_source_execs_fedctl_as_argv_list_shell_false` | List not string, `shell=False`, `capture_output=True`, a timeout is set, and the password is not in argv |
| `test_error_envelope_maps_code_to_http_status` | `HOST_NOT_ALLOWED` → 400, with message and hint passed through |
| `test_response_never_includes_a_password_field` | The word "password" appears nowhere in the serialised response |
| `test_list_sources_is_readable_without_can_manage` | Read works without manage; `canManage: false` is reported |
| `test_list_sources_reports_can_manage_true_for_privileged_user` | And `true` for a privileged one |

The third is the most valuable:

```go
assert isinstance(args[0], list)
assert kwargs["shell"] is False
assert "hunter2" not in " ".join(args[0])
sent = json.loads(kwargs["input"].decode("utf-8"))
assert sent == body
```

It asserts the password is absent from the arguments **and** present on
stdin. Both halves matter — checking only the first would pass if the
password were dropped entirely.

`assert "password" not in json.dumps(result)` is deliberately blunt: serialise
the whole response and search it. Crude, and exactly right for "must never
appear anywhere".

---

## [test/integration/federation_test.go](../test/integration/federation_test.go)

The end-to-end proof. It is excluded from normal runs by a build tag:

```go
//go:build integration
```

Without `-tags integration`, the Go toolchain does not even compile it. That
is how a test needing Docker, five minutes, and a real PostgreSQL can live in
the same repository as tests that run in two seconds.

What it does, in order:

1. Build `fedctl` from source.
2. `docker compose up -d --build` with the dev overlay and the test overlay.
3. Wait for health.
4. Create a small SQLite file **using the pure-Go driver** — so no `sqlite3`
   CLI is required on the test machine — and seed it into the volume with
   `fedctl seed sqlite`.
5. Attach all three sources by running `fedctl source add --json` **inside
   the Superset container**:

   ```go
   // execFedctl runs `fedctl <args...> --json` inside the running superset
   // container — the same path the Python shim uses — piping req as the
   // stdin request body, so source hostnames resolve on the docker network
   // exactly as they do in production.
   ```

   That detail is what makes this a real test. Running `fedctl` from the host
   would not resolve `dev-postgres` and would not exercise the code path the
   extension actually uses.

6. Create a native ClickHouse table with a fourth dataset.
7. Run the four-way join and assert on real rows:

   ```go
   if !found["Acme Corp|15000"] || !found["Acme Corp|4200"] {
       t.Errorf("expected Acme Corp's two orders in the joined result, got: %+v", got)
   }
   ```

   Not just "some rows came back" — those two specific orders, which only
   exist if PostgreSQL and MySQL were both really read and correctly joined.

8. Clean up:

   ```go
   t.Cleanup(func() { runDocker(t, root, "down", "--volumes") })
   ```

   `t.Cleanup` registers a function to run when the test finishes, however it
   finishes — including on failure. The stack is always torn down.

[compose.test.yaml](../test/integration/compose.test.yaml) is a ten-line
overlay that publishes ClickHouse's port 9000 as 19000 on the host, so the
test process (running outside Docker) can connect and assert directly. The
comment is careful to say why this is acceptable: it lives under
`test/integration`, is never referenced by `fedctl up`, and production still
publishes nothing.

---

## What the test suite tells you about the project

Read the tests as a list of things the author considered non-negotiable:

- Every SSRF rule, in both directions.
- Every path-traversal variant, including symlinks.
- No password in argv, in SQL, in logs, or in responses.
- Unauthorised users get 403 before anything executes.
- Idempotence and atomicity.
- Error codes map to the right HTTP statuses.
- The four-way join returns the right rows.

That list is essentially the acceptance criteria from section 14 of
[the spec](../superset-clickhouse-two-container-spec.md), turned into code.
When you write a specification with checkboxes, this is what the checkboxes
should end up as.

---

Next: [End-to-end walkthroughs](09-end-to-end-walkthroughs.md).
