# Releasing

How to cut a version and publish OS-native installers for Windows and Linux.

The short version, once everything below is set up:

```bash
make version-set VERSION=0.2.0
git commit -am "Release v0.2.0"
git tag v0.2.0
git push origin master v0.2.0
```

Pushing the tag is what triggers everything. Fifteen to twenty-five minutes
later there's a GitHub Release with installers attached and container images
on GHCR.

---

## One-time setup

You only do these once per repository.

### 1. Let the workflow publish packages

The release workflow pushes container images to the GitHub Container
Registry using the automatic `GITHUB_TOKEN`, so there's no secret to create
— but the repository has to allow it:

**Settings → Actions → General → Workflow permissions** → make sure
*"Read and write permissions"* is selected.

### 2. Make the images public after the first release

The first push creates the packages as **private**, which means users get
`denied` when Docker tries to pull them. After the first release completes,
go to the repository's **Packages** section, and for each of
`supersetmulticonnector/superset` and `supersetmulticonnector/clickhouse`:

**Package settings → Danger Zone → Change visibility → Public**

Skip this and `fedctl up` fails for everyone who isn't you. It only needs
doing once — later releases inherit the setting.

### 3. Confirm the workflow file is on your default branch

Tag-triggered workflows run from the tagged commit, so
`.github/workflows/release.yml` has to exist in that commit. If you're
adding it for the first time, push it to `master` *before* tagging.

---

## Rolling a release

### Step 1 — bump the version

```bash
make version-set VERSION=0.2.0
```

This rewrites every file that carries a copy of the version:

| File | What the copy controls |
|---|---|
| `VERSION` | the source of truth |
| `compose.yaml` | **which published images an installed copy pulls** |
| `extension/extension.json` | the extension manifest |
| `extension/backend/pyproject.toml` | the extension's Python package |
| `installer/windows/superset-federation.iss` | the installer's fallback version for local builds |

The `compose.yaml` entry is the one that really matters. It's shipped to
users, and it decides which containers they get — a release whose tag and
image tag disagree hands people the wrong software.

Verify before committing:

```bash
make version-check     # "all files agree on version 0.2.0"
make test
```

### Step 2 — commit and tag

```bash
git commit -am "Release v0.2.0"
git tag v0.2.0
git push origin master v0.2.0
```

The tag must be `v` + the exact contents of `VERSION`. The workflow checks
this first and refuses to build if they disagree, with a message telling you
what to run.

### Step 3 — wait, then make the images public if it's your first release

Watch it under the **Actions** tab. The jobs run mostly in parallel:

| Job | Produces | Roughly |
|---|---|---|
| `version` | the agreed version, and the consistency gate | seconds |
| `extension` | the `.supx` bundle | 2–4 min |
| `images` | multi-arch images pushed to GHCR | 10–20 min |
| `binaries` | 5 binaries, 6 Linux packages, checksums | 3–5 min |
| `installer-windows` | the `setup.exe` | 3–5 min |
| `release` | the GitHub Release with everything attached | seconds |

### Step 4 — verify it actually works

Don't skip this; it's the only thing that catches a broken installer.

```bash
# The images are pullable and public
docker pull ghcr.io/heavenlyraven/supersetmulticonnector/superset:0.2.0
docker pull ghcr.io/heavenlyraven/supersetmulticonnector/clickhouse:0.2.0

# The checksums match what was published
sha256sum -c checksums.txt --ignore-missing
```

Then install one of the artifacts on a clean machine or VM and run
first-time setup all the way to Superset loading in a browser. The
[installer README](installer/windows/README.md) has a fuller test checklist.

---

## What gets published

Every release attaches:

| Artifact | For |
|---|---|
| `superset-federation-<version>-windows-x64-setup.exe` | Windows |
| `superset-federation_<version>_amd64.deb`, `..._arm64.deb` | Debian, Ubuntu |
| `superset-federation-<version>.x86_64.rpm`, `...aarch64.rpm` | Fedora, RHEL, openSUSE |
| `superset-federation_<version>_x86_64.apk`, `..._aarch64.apk` | Alpine |
| `fedctl-{linux,darwin}-{amd64,arm64}`, `fedctl-windows-amd64.exe` | everyone else |
| `checksums.txt` | verifying any of the above |

Plus two multi-arch container images on GHCR, tagged with the version and
`latest`.

---

## Building artifacts locally

Useful for testing packaging changes without burning a tag.

### Linux packages

Needs [nfpm](https://nfpm.goreleaser.com/):

```bash
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
make packages-linux
ls dist/*.deb dist/*.rpm dist/*.apk
```

Test one for real:

```bash
sudo dpkg -i dist/superset-federation_0.2.0_amd64.deb
mkdir -p ~/sf && cp -r /usr/share/superset-federation/. ~/sf/ && cd ~/sf
fedctl install
```

### Windows installer

Needs [Inno Setup 6.3+](https://jrsoftware.org/isdl.php):

```bash
make installer-windows ISCC="C:/Program Files (x86)/Inno Setup 6/ISCC.exe"
ls dist/installer/
```

### Container images

```bash
docker build -f images/clickhouse/Dockerfile -t ghcr.io/heavenlyraven/supersetmulticonnector/clickhouse:dev .
docker build -f images/superset/Dockerfile   -t ghcr.io/heavenlyraven/supersetmulticonnector/superset:dev .

FEDERATION_CLICKHOUSE_IMAGE=ghcr.io/heavenlyraven/supersetmulticonnector/clickhouse:dev \
FEDERATION_SUPERSET_IMAGE=ghcr.io/heavenlyraven/supersetmulticonnector/superset:dev \
  fedctl up --no-build
```

---

## Troubleshooting

**"VERSION contains '0.1.0' but this release is '0.2.0'"**
You tagged without bumping. Delete the tag, run `make version-set`, commit,
re-tag:

```bash
git tag -d v0.2.0 && git push origin :refs/tags/v0.2.0
make version-set VERSION=0.2.0 && git commit -am "Release v0.2.0"
git tag v0.2.0 && git push origin master v0.2.0
```

**The `extension` job failed**
Expected until the bundler's package name is confirmed — see the `NOTE`
comment in [the workflow](.github/workflows/release.yml). The job is
`continue-on-error`, so the release still publishes; the images just ship
without the SQL Lab panel. Everything the panel does is available through
`fedctl source ...` regardless.

**Users get `denied` pulling the images**
The packages are still private. See one-time setup step 2.

**The Windows installer triggers SmartScreen**
Expected — it's unsigned. Users click *More info → Run anyway*. Fixing it
properly needs a code-signing certificate; see the
[installer README](installer/windows/README.md).

**`docker compose up` pulls the wrong version**
`compose.yaml`'s image tag disagrees with what was published. `make
version-check` catches this before a tag; if a bad release already went out,
bump to a new patch version rather than re-tagging — published tags may
already be cached on users' machines.

---

## Re-releasing the same version

Don't, if you can avoid it. Container tags may already be pulled and cached,
and package managers assume a given version's contents never change. Ship
`0.2.1` instead.

If you truly must (the release was broken within minutes and nobody has
installed it), delete the GitHub Release and its tag, then re-push. The
image tags will be overwritten, but anyone who already pulled keeps the old
layers.
