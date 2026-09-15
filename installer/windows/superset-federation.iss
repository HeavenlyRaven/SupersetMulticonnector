; Inno Setup script for the Superset + ClickHouse federation stack.
;
; Produces a per-user Windows installer that places fedctl.exe plus the
; runtime files it needs into a working directory, optionally adds it to
; PATH, and creates Start Menu shortcuts that open a terminal already
; positioned in that directory.
;
; Docker Desktop is a PREREQUISITE, not a payload: this installer only
; DETECTS it and points at docs/INSTALL.md if it's missing. Docker cannot
; be redistributed inside a third-party installer — it ships its own.
;
; Build with Inno Setup 6.3 or later:
;     iscc installer\windows\superset-federation.iss
; See installer/windows/README.md for the full build procedure.

#define AppName        "Superset Federation"
#define AppShortName   "SupersetFederation"

; Guarded so the release workflow's `ISCC /DAppVersion=1.2.3` wins. An
; unguarded #define would silently override the command line.
#ifndef AppVersion
  #define AppVersion   "0.1.1"
#endif
#define AppPublisher   "HeavenlyRaven"
#define AppURL         "https://github.com/HeavenlyRaven/SupersetMulticonnector"
#define FedctlExe      "fedctl.exe"

; Everything below is relative to this .iss file, which lives in
; installer/windows/ — hence the ..\..\ prefix back to the repo root.
#define RepoRoot       "..\.."

[Setup]
; A stable, unique identity for this application. Never change it, or
; upgrades will install alongside the old copy instead of replacing it.
AppId={{8F3A1C72-5D64-4E19-9B2A-7C6E4D1F8A03}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}/issues
AppUpdatesURL={#AppURL}/releases
VersionInfoVersion={#AppVersion}
VersionInfoDescription={#AppName} installer

; Per-user install: no administrator prompt, and — critically — the
; install directory stays writable, because `fedctl install` writes .env
; into it at first run. A Program Files install would need elevation for
; that write and fail for a normal user.
PrivilegesRequired=lowest
DefaultDirName={localappdata}\Programs\{#AppShortName}

; No space in the directory name on purpose: compose.yaml bind-mounts
; ./clickhouse/config.d and ./clickhouse/users.d into the ClickHouse
; container, and Docker Desktop on Windows has a long history of trouble
; with spaces in bind-mount source paths.
DisableDirPage=no
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes

OutputDir={#RepoRoot}\dist\installer
OutputBaseFilename=superset-federation-{#AppVersion}-windows-x64-setup
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\{#FedctlExe}
UninstallDisplayName={#AppName} {#AppVersion}

; fedctl-windows-amd64.exe is an x64 binary. x64compatible also permits
; ARM64 Windows, which runs it under emulation.
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "addtopath"; Description: "Add fedctl to my PATH (lets you run ""fedctl"" from any terminal)"; GroupDescription: "Optional:"
Name: "desktopicon"; Description: "Create a Desktop shortcut"; GroupDescription: "Optional:"; Flags: unchecked

[Files]
; --- The CLI itself ------------------------------------------------------
; Produced by `make cross-build` (see installer/windows/README.md).
Source: "{#RepoRoot}\dist\fedctl-windows-amd64.exe"; DestDir: "{app}"; DestName: "{#FedctlExe}"; Flags: ignoreversion

; --- Files fedctl reads from the working directory at runtime ------------
; `fedctl install` refuses to run unless compose.yaml AND .env.example are
; both present in the current directory (checkRepoCheckout in install.go).
Source: "{#RepoRoot}\compose.yaml";      DestDir: "{app}"; Flags: ignoreversion
Source: "{#RepoRoot}\compose.dev.yaml";  DestDir: "{app}"; Flags: ignoreversion
Source: "{#RepoRoot}\.env.example";      DestDir: "{app}"; Flags: ignoreversion

; --- Bind-mounted into the ClickHouse container at every start -----------
; These are NOT baked into the image; compose.yaml mounts them read-only
; from disk on each run, so they must exist next to compose.yaml.
Source: "{#RepoRoot}\clickhouse\config.d\*"; DestDir: "{app}\clickhouse\config.d"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#RepoRoot}\clickhouse\users.d\*";  DestDir: "{app}\clickhouse\users.d";  Flags: ignoreversion recursesubdirs createallsubdirs

; --- Optional runtime extras ---------------------------------------------
; config/sources.yaml powers `fedctl apply`; dev/ powers `fedctl up --dev`.
Source: "{#RepoRoot}\config\*"; DestDir: "{app}\config"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#RepoRoot}\dev\*";    DestDir: "{app}\dev";    Flags: ignoreversion recursesubdirs createallsubdirs

; --- No image build inputs, deliberately -----------------------------------
; Nothing from cmd/, internal/, images/ or superset/ is shipped. compose.yaml
; names published images on GHCR, and `fedctl up` detects the absence of
; images/*/Dockerfile and pulls those instead of building (resolveBuildMode
; in cmd/fedctl/up.go). So the only thing a user needs on their machine is
; Docker — no Go toolchain, no Node, no Python, and no five-minute first-run
; image build.
;
; A consequence worth knowing: `fedctl up --build` cannot work from an
; installed copy, because there is no source here to build from. That flag
; is for people working in a git clone.

; --- Documentation --------------------------------------------------------
; No `isreadme` flag: the [Run] section already offers to open this, and
; isreadme would make it appear twice.
Source: "{#RepoRoot}\docs\INSTALL.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#RepoRoot}\README.md";       DestDir: "{app}"; Flags: ignoreversion

[Icons]
; Every shortcut opens cmd.exe with {app} as the working directory,
; because fedctl resolves compose.yaml and .env relative to the current
; directory — it has no notion of an install location of its own.
Name: "{group}\1. First-time setup"; Filename: "{cmd}"; Parameters: "/k cd /d ""{app}"" && ""{app}\{#FedctlExe}"" install"; WorkingDir: "{app}"; Comment: "Check Docker, write .env, build the images, start the stack"
Name: "{group}\2. Start the stack";  Filename: "{cmd}"; Parameters: "/k cd /d ""{app}"" && ""{app}\{#FedctlExe}"" up";      WorkingDir: "{app}"; Comment: "Bring both containers up and wait for them to become healthy"
; An Internet shortcut: Inno requires the Name to end in .url when the
; Filename is a URL rather than a local path.
Name: "{group}\3. Open Superset.url"; Filename: "http://localhost:8088"
Name: "{group}\Stop the stack";      Filename: "{cmd}"; Parameters: "/k cd /d ""{app}"" && ""{app}\{#FedctlExe}"" down";    WorkingDir: "{app}"; Comment: "Stop both containers (data is kept)"
Name: "{group}\Check the stack";     Filename: "{cmd}"; Parameters: "/k cd /d ""{app}"" && ""{app}\{#FedctlExe}"" doctor";  WorkingDir: "{app}"; Comment: "Probe every federated source and print a status table"
Name: "{group}\Terminal here";       Filename: "{cmd}"; Parameters: "/k cd /d ""{app}"""; WorkingDir: "{app}"; Comment: "A command prompt in the installation directory"
Name: "{group}\Installation guide";  Filename: "{app}\INSTALL.md"
Name: "{group}\Uninstall {#AppName}"; Filename: "{uninstallexe}"

Name: "{autodesktop}\{#AppName}"; Filename: "{cmd}"; Parameters: "/k cd /d ""{app}"""; WorkingDir: "{app}"; Tasks: desktopicon

[Registry]
; Per-user PATH lives in HKCU\Environment. Inno broadcasts the settings
; change itself, so open terminals pick it up after a restart.
; Inno expands {app} inside the Check parameter before passing it, so the
; function receives a real path. A nested ExpandConstant() call here would
; not compile — Check parameters take literals only.
Root: HKCU; Subkey: "Environment"; ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; Check: NeedsAddPath('{app}'); Tasks: addtopath

[Run]
Filename: "{cmd}"; Parameters: "/k cd /d ""{app}"" && ""{app}\{#FedctlExe}"" install"; WorkingDir: "{app}"; Description: "Run first-time setup now (checks Docker, writes .env, builds the images)"; Flags: postinstall nowait unchecked
Filename: "{app}\INSTALL.md"; Description: "Open the installation guide"; Flags: postinstall nowait shellexec unchecked

[UninstallDelete]
; .env is generated by `fedctl install`, not shipped, so Inno will not
; remove it on its own. Same for anything else written after install.
Type: files; Name: "{app}\.env"
Type: dirifempty; Name: "{app}"

[Code]

// PATH handling: only append the install directory if it is not already
// present, so repeated installs don't grow the variable without bound.
//
// This is a `//` comment, not a `{ }` one, on purpose: Pascal Script's
// brace comments don't nest, and the install-directory placeholder is
// itself written {app} — its own closing brace would silently end a { }
// comment early, dumping the rest of the sentence into real code and
// producing a baffling "'BEGIN' expected" error pointing at prose, not a
// bug. `//` comments have no closing delimiter to collide with.
function NeedsAddPath(Param: string): Boolean;
var
  OrigPath: string;
begin
  if not RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', OrigPath) then
  begin
    Result := True;
    Exit;
  end;
  // Pad with semicolons so a partial match can't produce a false positive.
  Result := Pos(';' + Lowercase(Param) + ';', ';' + Lowercase(OrigPath) + ';') = 0;
end;

// Docker detection. This checks only that the `docker` CLI answers — not
// that the daemon is running, which is a separate condition `fedctl
// preflight` reports properly at first run.
function IsDockerPresent(): Boolean;
var
  ResultCode: Integer;
begin
  Result := Exec('cmd.exe', '/C docker --version >nul 2>&1', '', SW_HIDE,
                 ewWaitUntilTerminated, ResultCode) and (ResultCode = 0);
end;

function InitializeSetup(): Boolean;
begin
  Result := True;
  if IsDockerPresent() then
    Exit;

  if MsgBox(
      'Docker Desktop was not detected on this computer.' + #13#10#13#10 +
      'Superset Federation runs as two Docker containers, so Docker Desktop has to be ' +
      'installed and running before the stack can start. It is a separate download from ' +
      'Docker, and this installer cannot include it.' + #13#10#13#10 +
      'You can install Superset Federation now and set Docker up afterwards — the ' +
      'installation guide included with it explains exactly how.' + #13#10#13#10 +
      'Continue with the installation?',
      mbConfirmation, MB_YESNO) = IDNO then
    Result := False;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    if not IsDockerPresent() then
      MsgBox(
        'Installation finished.' + #13#10#13#10 +
        'Remember: Docker Desktop still needs to be installed before the stack will start. ' +
        'Open "Installation guide" in the Start Menu folder for step-by-step instructions, ' +
        'then run "1. First-time setup".',
        mbInformation, MB_OK);
  end;
end;
