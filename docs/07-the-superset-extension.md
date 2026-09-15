# 7. The Superset extension

The extension is what puts a "Federated sources" panel inside SQL Lab, so a
person can add a database source by filling in a form instead of typing CLI
commands.

It has two halves, in two languages, because Superset's extension framework
requires both:

- **The backend** is Python, because Superset loads
  `backend/src/{publisher}/{name}/entrypoint.py` through its own Python
  machinery.
- **The frontend** is TypeScript/React, because Superset loads it through
  Webpack Module Federation into its own React application.

Neither can be Go. That is the entire reason Python and TypeScript appear in
a project that is otherwise deliberately all Go — and the design response is
to keep both halves as thin as possible.

```
Browser (React panel)
   │  fetch('/extensions/acme/ch-federation/sources', {...})
   ▼
entrypoint.py  ── checks permission ──▶ 403 if not allowed
   │
   │  subprocess.run([fedctl, "source", "add", "--json"], input=<JSON on stdin>)
   ▼
fedctl  ── validates, generates SQL, executes against ClickHouse ──▶ {"ok": true, "data": {...}}
   │
   ▼
entrypoint.py  ── maps error code to HTTP status, writes audit log ──▶ JSON response
   │
   ▼
Browser updates the table
```

---

## [extension/extension.json](../extension/extension.json) — the manifest

```json
{
  "publisher": "acme",
  "name": "ch-federation",
  "displayName": "ClickHouse Federation",
  "version": "0.1.0",
  "license": "Apache-2.0",
  "permissions": ["can_manage on ChFederationAPI", "can_read on ChFederationAPI"]
}
```

`publisher` and `name` together determine everything else: the Python package
path (`src/acme/ch_federation/`), the REST prefix
(`/extensions/acme/ch-federation/`), and the Module Federation container
name. Change one and you must change all of them.

The two declared permissions are Flask-AppBuilder permission strings.
Superset's **Admin** role gets them automatically; **Alpha** does not, which
the README flags as a known limitation — grant it by hand in Settings → List
Roles if you want Alpha users managing sources.

---

## [entrypoint.py](../extension/backend/src/acme/ch_federation/entrypoint.py) — the shim

235 lines, nine endpoints, and — by design — no business logic whatsoever.
Its own docstring states the rule:

> No DDL, no validation, no ClickHouse client — if you find yourself writing
> logic here, move it to Go.

### The subprocess call

Everything funnels through one function:

```python
def _exec_fedctl(args, stdin_bytes, timeout):
    try:
        proc = subprocess.run(
            [FEDCTL_BIN, *args, "--json"],
            input=stdin_bytes,
            capture_output=True,
            shell=False,
            timeout=timeout,
        )
    except subprocess.TimeoutExpired:
        return None, {"code": "INTERNAL_ERROR", "message": "fedctl timed out", "hint": ""}
```

Four safety properties in one call:

1. **A list, not a string.** `subprocess.run("fedctl source add")` would go
   through a shell and be vulnerable to injection. A list is passed straight
   to `execve`. If a value contains a semicolon, it is just a semicolon.
2. **`shell=False`.** Explicit, even though it is the default — this is a
   documented requirement, so it is written down.
3. **An explicit timeout.** A hung `fedctl` would otherwise hold a gunicorn
   worker forever. Note there are two timeouts: 30 seconds normally, 120 for
   uploads, because writing a 200 MB file legitimately takes longer than
   running DDL.
4. **`input=stdin_bytes`.** Credentials and file contents go on stdin. Never
   argv, never an environment variable.

Then the response envelope is parsed:

```python
envelope = json.loads(proc.stdout.decode("utf-8") or "{}")
if envelope.get("ok"):
    return envelope.get("data"), None
return None, envelope.get("error") or {"code": "INTERNAL_ERROR", "message": "unknown error"}
```

This is the Python side of the contract defined in
[jsonio.go](../internal/jsonio/jsonio.go). Note that stderr is only used for
the hint when stdout could not be parsed at all — `fedctl`'s diagnostic
logging never interferes with the response.

### Mapping errors to HTTP

```python
ERROR_STATUS = {
    "INVALID_REQUEST": 400,
    "HOST_NOT_ALLOWED": 400,
    "SOURCE_EXISTS": 409,
    "SOURCE_NOT_FOUND": 404,
    "FILE_EXISTS": 409,
    "CONNECTION_FAILED": 502,
    "HUB_UNAVAILABLE": 502,
    "INTERNAL_ERROR": 500,
    ...
}
```

Each Go error code becomes a proper HTTP status. `409 Conflict` for "already
exists", `502 Bad Gateway` for "the upstream database is unreachable" — the
latter being exactly right, since the failure really is in an upstream
service.

Message and hint are passed through **unchanged**. The Python layer never
rewrites them, so the text a CLI user sees and the text a browser user sees
are the same text, produced in one place.

### Permission checks

```python
def _can_manage(self):
    return self.appbuilder.sm.has_access("can_manage", "ChFederationAPI")

@expose("/sources", methods=("POST",))
@protect()
@permission_name("manage")
def add_source(self):
    if not _can_manage(self):
        return self.response_403()
    ...
```

Three layers on a mutating endpoint:

1. `@protect()` — Flask-AppBuilder's authentication requirement.
2. `@permission_name("manage")` — declares which permission this maps to.
3. An explicit `if not _can_manage(self)` check **before** any subprocess
   runs.

The third looks redundant. It is not: it is the one that is unit-tested, and
the test asserts something specific — that `fedctl` is *never executed* for
an unauthorised user:

```python
def test_add_source_denied_without_can_manage_and_never_execs_fedctl():
    ...
    assert result["status"] == 403
    run.assert_not_called()
```

Read endpoints (`GET /sources`, `/source-types`, `/sources/{name}/tables`)
require only `read`, so a less-privileged user can *see* the sources without
being able to change them.

### `canManage` in the list response

```python
return self.response(200, result={"sources": data, "canManage": _can_manage(self)})
```

The UI uses this to hide the Add and Remove buttons for users who cannot use
them. The comment is emphatic about what it is *not*:

> the server still enforces the same check independently on every mutating
> call, this is UX only.

Hiding a button is not a security control. The server checks again, every
time, regardless of what the client believes.

### The audit log

```python
def _audit(action, **fields):
    """user, source name, type, host, timestamp — never credentials."""
    audit_log.info("%s user=%s %s", action,
        getattr(current_user, "username", "unknown"),
        " ".join(f"{k}={v}" for k, v in fields.items()),
        extra={"timestamp": datetime.now(timezone.utc).isoformat()})
```

Every mutation is logged with who did it, to what, and when. Never with
credentials. This is why `doRemoveSource` in Go goes to the trouble of
reading the host *before* dropping the database — the audit entry needs it.

### The `resource_name` comment

```python
class ChFederationAPI(RestApi):
    # Deliberately no resource_name: live-verified against the installed
    # apache-superset-core package — passing one adds an extra path segment...
```

A note about a specific version of a pre-1.0 framework, recording that
passing `resource_name` would insert an extra path segment and break every
URL the frontend expects. Exactly the kind of thing that is impossible to
rediscover from documentation.

### The upload endpoint

The one endpoint that does not take JSON:

```python
uploaded = request.files.get("file")
name = (request.form.get("name") or uploaded.filename or "").strip()
overwrite = request.form.get("overwrite", "").strip().lower() in ("1", "true", "yes")
args = ["source", "sqlite-files", "put", "--name", name]
if overwrite:
    args.append("--replace")
data, err = _run_fedctl_upload(args, uploaded.stream.read())
```

`multipart/form-data`, with the file's raw bytes piped straight to `fedctl`'s
stdin. The filename is passed as an argument (it is not a secret), and
`fedctl` validates it against `seedNameRe` — the Python side does not attempt
its own validation, consistent with the "no logic here" rule.

---

## The frontend

### [index.tsx](../extension/frontend/src/index.tsx) — registration

```tsx
views.registerView(
  { id: 'acme.ch-federation.panel', name: 'Federated sources' },
  'sqllab.panels',
  () => <FederationPanel />,
);
```

Eighteen lines including comments. It tells Superset: "here is a view, put it
in SQL Lab's panels area." Superset renders it as a tab.

The comment notes a limitation: the available contribution points are
`sqllab.leftSidebar`, `.editor`, `.rightSidebar`, and `.panels` — there is
currently nowhere outside SQL Lab to put a full admin panel. So the panel
lives in SQL Lab, and the README records why.

### [types.ts](../extension/frontend/src/types.ts) — the shared vocabulary

TypeScript interfaces mirroring the Go structs:

```ts
export interface SourceSummary {
  name: string;
  type: string;
  database: string;
  tableCount: number;
  host?: string;
}
```

Compare with `hub.SourceSummary` in Go — same fields, same JSON names. These
two definitions are not linked by any tool; keeping them in sync is manual.
When you add a field in Go, you add it here too.

The `?` marks an optional field, matching Go's `omitempty`.

At the bottom, a **type guard**:

```ts
export function isApiError(e: unknown): e is ApiError {
  return typeof e === 'object' && e !== null && 'message' in e;
}
```

The return type `e is ApiError` tells the TypeScript compiler that inside an
`if (isApiError(e))` block, `e` may be treated as an `ApiError`. This is how
you narrow the type of a caught exception, which TypeScript types as
`unknown` because anything can be thrown.

### [api.ts](../extension/frontend/src/api.ts) — the HTTP client

```ts
const API_BASE = '/extensions/acme/ch-federation';
```

That prefix comes from `extension.json`'s publisher and name. The comment
warns that the framework is pre-1.0 and this prefix should be the first
suspect if requests start returning 404.

The `request` helper handles CSRF:

```ts
if (isMutation) {
  const token = await authentication.getCSRFToken();
  if (token) headers['X-CSRFToken'] = token;
}
```

A fresh token on every state-changing request. If it comes back undefined,
the header is simply omitted and the request fails with Superset's own CSRF
error — which is a clearer message than a client-side crash would be.

`credentials: 'same-origin'` sends the session cookie, without which the
server would see an anonymous request.

Error handling unwraps the shim's response shape:

```ts
if (!resp.ok) {
  const err: ApiError = {
    message: (body.message as string) || `request failed with status ${resp.status}`,
    hint: body.hint as string | undefined,
  };
  throw err;
}
return (('result' in body ? body.result : body) as T);
```

The `message`/`hint` pair travels all the way from a Go `jsonio.NewError`
call to the red box in the browser, unmodified at every hop.

Uploads need their own function:

```ts
async function uploadRequest<T>(path: string, formData: FormData): Promise<T> {
  const headers: Record<string, string> = {};   // deliberately no Content-Type
  ...
}
```

The comment explains why: `fetch` generates the correct
`multipart/form-data; boundary=...` header **only if you leave Content-Type
unset** and pass a `FormData` body. Setting it yourself produces a request
with no boundary parameter, which the server cannot parse. A classic
one-line-fix bug, documented so nobody "helpfully" adds the header back.

### [FederationPanel.tsx](../extension/frontend/src/FederationPanel.tsx) — the main view

A standard React component. If React is new to you, the essentials:

```tsx
const [sources, setSources] = useState<SourceSummary[]>([]);
```

`useState` declares a piece of state and a setter for it. Calling the setter
re-renders the component. `<SourceSummary[]>` types the state as an array of
sources.

```tsx
const refresh = useCallback(async () => { ... }, []);

useEffect(() => { refresh(); }, [refresh]);
```

`useEffect` runs code *after* rendering — here, to load data once the
component appears. `useCallback` keeps `refresh` stable between renders so
the effect does not fire repeatedly. The `[refresh]` array is the dependency
list: re-run only when those change.

The data load fetches both things at once:

```tsx
const [list, types] = await Promise.all([api.listSources(), api.sourceTypes()]);
```

`Promise.all` runs both requests concurrently rather than one after the
other.

The read-only mode:

```tsx
{canManage ? (
  <button style={buttonStyle('primary')} onClick={() => setShowAdd(true)}>Add source</button>
) : (
  <span title="Your role does not include manage access for federated sources">Read-only</span>
)}
```

Note it shows a labelled "Read-only" indicator rather than silently hiding
the button — the user learns *why* they cannot do something.

The health dot is per-source local state, and starts as `unknown`:

```tsx
const color = { unknown: '#999', checking: '#1890ff', healthy: '#52c41a', unhealthy: '#f5222d' }[state ?? 'unknown'];
```

Health is never assumed; it is only known after you click Probe. That is
honest UI: the panel does not show a green dot it has not verified.

### [AddSourceModal.tsx](../extension/frontend/src/AddSourceModal.tsx) — the self-generating form

This is the component that makes "add a source type without touching the
frontend" real:

```tsx
{activeType?.fields.map((field) => (
  <label style={labelStyle()} key={field.name}>
    {field.label}{field.required ? ' *' : ''}
    {field.type === 'path' ? (
      <SqliteFileField value={params[field.name] ?? ''} onChange={(v) => setField(field.name, v)} />
    ) : (
      <input type={field.type === 'password' ? 'password' : 'text'} ... />
    )}
    {field.helperText && <div style={helperTextStyle()}>{field.helperText}</div>}
  </label>
))}
```

The form is a `map` over whatever the server said the fields are. There is no
`if (type === 'postgres')` anywhere. The only type-specific branch is on the
*field type* `path`, which swaps in the file picker — and even that is
declared by the Go code, not assumed here.

`type="password"` on password fields keeps them out of the visible DOM text
and out of browser autofill history.

Defaults are seeded into state, not merely displayed:

```tsx
function defaultParamsFor(ty: SourceTypeInfo | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  ty?.fields.forEach((f) => { if (f.default) out[f.name] = f.default; });
  return out;
}
```

The comment explains the bug this prevents: if the default were only shown as
placeholder text, a user who accepts "5432" without retyping it would submit
an empty port.

### Test-before-add, and the signature trick

The requirement is that Test connection must succeed before Add is enabled.
The naive implementation has a hole: test successfully, then edit the
hostname, then add — you just added an untested configuration.

The fix is neat:

```tsx
const signature = useMemo(() => JSON.stringify({ typeName, name, params }), [typeName, name, params]);
const canAdd = testState === 'ok' && testedSignature === signature;
```

The entire form state is serialised into a string. A successful test records
that string. **Add is enabled only while the current form still matches the
tested one.** Change any character and `canAdd` becomes false again. One
comparison, no per-field bookkeeping.

### [SqliteFileField.tsx](../extension/frontend/src/SqliteFileField.tsx) — the file picker

Three controls: a dropdown of files already in the volume, an Upload button,
and a Replace button.

The dropdown lists whatever `fedctl source sqlite-files list` returns —
files that arrived by CLI seeding *and* files uploaded through the browser,
because both write into the same directory.

The hidden-input pattern for file uploads:

```tsx
<button onClick={() => uploadInputRef.current?.click()}>Upload new file…</button>
<input ref={uploadInputRef} type="file" style={{ display: 'none' }} onChange={handleUploadChosen} />
```

Native file inputs cannot be styled. So the real input is hidden, a normal
button is shown, and clicking the button programmatically clicks the input.
Standard practice.

```tsx
e.target.value = ''; // let the same file be re-selected later if needed
```

Without this, choosing the same file twice fires no `change` event the second
time, and the UI would appear frozen.

**Upload versus Replace** is the conceptual heart of this component:

```tsx
// Target name is the currently selected file, not the picked file's own
// name — the point of Replace is refreshing the same source's data without
// changing what its ClickHouse database points at.
const result = await api.uploadSqliteFile(file, value, true);
```

- **Upload** adds a new file under its own name.
- **Replace** overwrites the *currently selected* file's bytes, whatever the
  new file happens to be called.

Why does Replace exist at all? Because SQLite is a file, not a service.
PostgreSQL and MySQL sources re-read live data on every query; a SQLite
source reads whatever is in the file. To refresh it, you replace the bytes —
while keeping the filename, so the ClickHouse database definition pointing at
that path stays valid.

That single product requirement is why `fedctl source sqlite-files put` has a
`--replace` flag, why the API takes `overwrite`, and why the shim maps it.
One idea, consistently threaded through three layers.

### [RemoveSourceModal.tsx](../extension/frontend/src/RemoveSourceModal.tsx)

```tsx
<button disabled={typed !== name || removing}>Remove</button>
```

You must type the exact source name to enable the button — the same pattern
GitHub uses for deleting a repository. A destructive, hard-to-undo action
should require more than one click.

### [Modal.tsx](../extension/frontend/src/Modal.tsx)

A hand-rolled modal, because Superset's component library does not export one
yet:

```tsx
onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
```

`e.target` is what was actually clicked; `e.currentTarget` is the element the
handler is attached to. They are equal only when you clicked the backdrop
itself and not something inside the dialog — which is how "click outside to
close" works without closing on every click within.

### [styles.ts](../extension/frontend/src/styles.ts) — and a real visual bug

The most instructive comment in the frontend:

> Live-verified bug this fixes: Modal's panel had a hardcoded white
> background, but its title/labels/helper text had no color set, so they
> inherited Superset's page-wide text color — which is near-white when
> Superset is in dark mode. White text on a white panel is invisible.

The rule that came out of it: **every style sets both background and text
colour explicitly**. Setting one and inheriting the other is how you get
invisible text.

Since the component library exposes no theme tokens, the theme is detected
from the page itself:

```ts
const bg = window.getComputedStyle(document.body).backgroundColor;
const [r, g, b] = m.map(Number);
// Perceived brightness (ITU-R BT.601) — below the midpoint counts as dark.
return (r * 299 + g * 587 + b * 114) / 1000 < 128;
```

That formula weights green most and blue least, because human eyes are most
sensitive to green. It is the standard luminance approximation.

```ts
// Not memoized: cheap (a single getComputedStyle call), and re-reading it
// per render means a Superset theme toggle takes effect immediately.
export function getTheme(): Palette { return isDarkHost() ? dark : light; }
```

A deliberate decision *not* to optimise, with the reason given. The dark
palette values are taken from antd v5's dark theme, since Superset is
antd-based — so the panel looks native rather than approximately right.

### [webpack.config.js](../extension/frontend/webpack.config.js) — Module Federation

**Module Federation** lets a running application load code that was built
separately, at runtime. That is how a Superset instance you did not build can
load a panel you did.

```js
new ModuleFederationPlugin({
  name: "acme_chFederation",
  filename: "remoteEntry.[contenthash].js",
  exposes: { "./index": "./src/index.tsx" },
  shared: {
    react: { singleton: true, requiredVersion: packageConfig.peerDependencies.react, import: false },
    "react-dom": { singleton: true, ..., import: false },
  },
})
```

- `exposes` — Superset asks for the module `./index`, so that name must match
  what `index.tsx` provides.
- `shared: { react: { singleton: true } }` — use the host's React, do not
  bundle your own. Two copies of React in one page breaks hooks in confusing
  ways. `import: false` means "never provide it, only consume it".

```js
externalsType: "window",
externals: { "@apache-superset/core": "superset" },
```

`@apache-superset/core` is not bundled and not federated — the host app
places it on `window.superset`, so imports are rewritten to read that global.
This is why every component imports from the single root specifier
`@apache-superset/core` rather than a subpath: only the bare specifier is
externalised.

```js
publicPath: `/api/v1/extensions/${extensionConfig.publisher}/${extensionConfig.name}/`,
```

Where the browser fetches additional chunks from — built from the manifest,
so it cannot drift.

### [package.json](../extension/frontend/package.json) and [tsconfig.json](../extension/frontend/tsconfig.json)

React 17 appears under `peerDependencies`, not `dependencies` — "the host
must provide this", matching the Module Federation setup.

The TypeScript config sets `"strict": true`, which turns on null checking and
several other checks. It is why you see `?.` and `??` throughout the
components: the compiler forces the possibility of undefined to be handled.

---

## What is *not* in this extension

Worth noticing, because it is the design working:

- No SQL.
- No validation rules.
- No knowledge of PostgreSQL, MySQL, or SQLite specifics.
- No ClickHouse client.
- No hardcoded form fields.

All of that is in Go, reachable from both the CLI and the browser. The
extension is a window onto `fedctl`, not a reimplementation of it.

---

Next: [The tests](08-tests.md).
