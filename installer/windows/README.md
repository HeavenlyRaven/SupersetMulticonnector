# Building the Windows installer

This directory holds the [Inno Setup](https://jrsoftware.org/isinfo.php)
script that packages `fedctl.exe` plus the runtime files it needs into a
single `setup.exe`.

For *using* the installer, see [docs/INSTALL.md](../../docs/INSTALL.md).
This file is for whoever builds it.

## What the installer contains — and deliberately doesn't

**Contains:**

- `fedctl.exe`, built for `windows/amd64`
- The files `fedctl` reads from its working directory: `compose.yaml`,
  `compose.dev.yaml`, `.env.example`
- The ClickHouse configuration that gets bind-mounted into the container at
  every start (`clickhouse/config.d/`, `clickhouse/users.d/`) — these are
  *not* baked into any image, so they must exist on disk
- `config/sources.yaml` and `dev/` for the optional declarative and sample-
  data workflows

**Deliberately does not contain:**

- **Docker Desktop.** It cannot be redistributed inside a third-party
  installer. The script only *detects* it and points at the installation
  guide when missing.
- **Any Go source, Dockerfiles, or `superset_config.py`.** `compose.yaml`
  names published images on GHCR, and `resolveBuildMode` in
  [cmd/fedctl/up.go](../../cmd/fedctl/up.go) notices that
  `images/*/Dockerfile` is absent and pulls them rather than building. So
  the installed machine needs Docker and nothing else — no Go toolchain, no
  Node, no Python, and no first-run image build.
- **The `.supx` extension bundle**, which is baked into the published
  Superset image instead (the release workflow builds it and drops it in
  `extension/bundle/`, which the Dockerfile copies to `/app/extensions`).

## Prerequisites

1. **Inno Setup 6.3 or later** — <https://jrsoftware.org/isdl.php>.
   6.3 is the minimum because the script uses `ArchitecturesAllowed=x64compatible`.
   After installing, `ISCC.exe` lands in
   `C:\Program Files (x86)\Inno Setup 6\`.
2. **A fresh `fedctl-windows-amd64.exe` in `dist/`**, which the script
   expects to find. Produce it from the repo root:

   ```
   make cross-build
   ```

   or, for just this one target:

   ```
   CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=0.1.0" -o dist/fedctl-windows-amd64.exe ./cmd/fedctl
   ```

   Check what you're about to ship — `dist/` is git-ignored and can easily
   hold a stale binary from weeks ago:

   ```
   ls -la dist/fedctl-windows-amd64.exe
   ```

## Building

From the repository root:

```
"C:\Program Files (x86)\Inno Setup 6\ISCC.exe" installer\windows\superset-federation.iss
```

or, if `ISCC.exe` is on your PATH:

```
iscc installer\windows\superset-federation.iss
```

The output lands in `dist/installer/`:

```
dist/installer/superset-federation-0.1.0-windows-x64-setup.exe
```

## Version bumps

A tagged release sets the version for you — the workflow passes
`/DAppVersion=` to ISCC, which wins over the `#define` in the script (that's
why the define is wrapped in `#ifndef`). It's only the fallback for local
builds.

These do need updating by hand, and must agree with the tag:

| File | Field |
|---|---|
| `compose.yaml` | the `:0.1.0` tag in both `image:` defaults |
| `extension/extension.json` | `"version"` |
| `extension/backend/pyproject.toml` | `version` |
| `Makefile` | `PKG_VERSION` (the Linux packages' fallback) |
| `installer/windows/superset-federation.iss` | `#define AppVersion` (local-build fallback) |

The `compose.yaml` one matters most: it decides which published image an
installed copy pulls, so a release whose tag and image tag disagree ships
users the wrong containers.

Never change `AppId` in the `.iss`. Windows uses it to recognise an
existing installation — change it and the next installer will install
*alongside* the old copy instead of upgrading it.

## Testing the result

Worth doing on a clean machine or VM, since the whole point is the
first-time experience:

1. **Without Docker installed** — the warning dialog should appear and let
   you continue, and a reminder should follow at the end.
2. **With Docker installed** — no warning at all.
3. Confirm the Start Menu folder appears with all its shortcuts.
4. Run **"1. First-time setup"** and go all the way through to Superset
   loading in a browser.
5. Open a *new* terminal and run `fedctl version` — proves the PATH entry
   took effect (it only applies to terminals opened after installing).
6. Uninstall, and confirm the install directory and Start Menu folder are
   both gone.

## Code signing

The installer is unsigned, so Windows SmartScreen shows
**"Windows protected your PC"** on first run. Users can click through via
*More info → Run anyway*, but it undercuts the point of shipping a polished
installer.

Fixing it properly needs a code-signing certificate from a CA (an OV
certificate runs roughly $200–400/year; an EV certificate clears SmartScreen
immediately but costs more and usually requires a hardware token). With one
in hand, add to the `[Setup]` section:

```
SignTool=signtool sign /f "path\to\cert.pfx" /p $p /tr http://timestamp.digicert.com /td sha256 /fd sha256 $f
```

and pass the password at build time with `ISCC /Sp=<password>`.

Until then, publish the SHA-256 of the installer alongside it on the release
page so people can verify the download by hand.

## Releasing

You rarely need to build this by hand — [the release
workflow](../../.github/workflows/release.yml) compiles the installer on a
Windows runner (installing Inno Setup via Chocolatey), alongside the
container images, the five `fedctl` binaries, and the Linux packages, and
attaches everything to a GitHub Release. Push a tag to trigger it:

```
git tag v0.1.1 && git push origin v0.1.1
```

Building locally is for testing changes to the `.iss` itself.

## Other platforms

Linux is covered by [packaging/nfpm.yaml](../../packaging/nfpm.yaml), which
produces `.deb`, `.rpm` and `.apk` — `make packages-linux`.

macOS has no native installer yet. A `.pkg` or an app bundle in a `.dmg` are
both plausible, but either needs an Apple Developer account ($99/year) and
notarization, or Gatekeeper blocks it. Until then
[docs/INSTALL.md](../../docs/INSTALL.md) documents the prebuilt-binary path.
