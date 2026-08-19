#ifndef AppVer
  #define AppVer "dev"
#endif

[Setup]
AppName=AzerothCore Playerbots Server
AppVersion={#AppVer}
DefaultDirName={autopf}\Playerbots
DefaultGroupName=Playerbots
OutputDir=output
OutputBaseFilename=mod-playerbots-portable-{#AppVer}
Compression=lzma2/ultra64
SolidCompression=yes
ArchitecturesInstallIn64BitMode=x64compatible
DisableProgramGroupPage=yes
DisableReadyPage=yes
UsePreviousTasks=yes

[Files]
Source: "dist\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Dirs]
Name: "{app}"; Permissions: users-full

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Icons]
Name: "{group}\Playerbots Server Launcher"; Filename: "{app}\startup.exe"; WorkingDir: "{app}"
Name: "{commondesktop}\Playerbots Server Launcher"; Filename: "{app}\startup.exe"; WorkingDir: "{app}"; Tasks: desktopicon

[Run]
Filename: "{app}\startup.exe"; Description: "Launch Playerbots Server"; Flags: nowait postinstall skipifsilent

