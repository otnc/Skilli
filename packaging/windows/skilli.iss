#ifndef AppVersion
  #define AppVersion "0.2.0-dev"
#endif
#ifndef SourceDir
  #define SourceDir "..\..\dist"
#endif
#define RepoDir "..\.."

[Setup]
AppId=otnc.Skilli
AppName=Skilli
AppVersion={#AppVersion}
AppPublisher=otoneko.
DefaultDirName={localappdata}\Programs\Skilli
DefaultGroupName=Skilli
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
; Inno Setup 6.7+ enables RedirectionGuard on Setup, and without elevation the
; app started at the end inherits it; it then cannot follow junctions that
; tools such as vite-plus or fnm create (Windows error 448).
RedirectionGuard=no
MinVersion=10.0.22000
ArchitecturesAllowed=x64os
ArchitecturesInstallIn64BitMode=x64os
CloseApplications=yes
RestartApplications=no
UninstallDisplayIcon={app}\skilli-gui.exe
WizardStyle=modern
OutputDir=..\..\dist
OutputBaseFilename=Skilli-Setup-{#AppVersion}-x64
LicenseFile={#RepoDir}\TERMS.txt

[Languages]
Name: "ja"; MessagesFile: "compiler:Languages\Japanese.isl"

[Files]
Source: "{#SourceDir}\skilli.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\skilli-gui.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\.mygo-files"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist
Source: "{#SourceDir}\icon.png"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist
Source: "{#RepoDir}\LICENSE"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion
Source: "{#RepoDir}\TERMS.txt"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\Skilli"; Filename: "{app}\skilli-gui.exe"; WorkingDir: "{app}"
Name: "{autoprograms}\Skilli 利用規約"; Filename: "{app}\TERMS.txt"

[Run]
Filename: "{app}\skilli-gui.exe"; Description: "Skilli を起動する"; Flags: nowait postinstall skipifsilent

[Code]
const
  RunKey = 'Software\Microsoft\Windows\CurrentVersion\Run';
  ApprovedKey = 'Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run';

{ The app registers itself to start at sign-in under its MyGo identifier:
  com.otnc.skilli in release builds, Skilli in local builds. }
procedure RemoveStartAtLogin(Name: String);
begin
  RegDeleteValue(HKEY_CURRENT_USER, RunKey, Name);
  RegDeleteValue(HKEY_CURRENT_USER, ApprovedKey, Name);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
  begin
    RemoveStartAtLogin('com.otnc.skilli');
    RemoveStartAtLogin('Skilli');
  end;
end;
