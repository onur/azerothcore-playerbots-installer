[Setup]
AppName=Playerbots AzerothCore Server
AppVersion=0.1.0
DefaultDirName={autopf}\Playerbots
DefaultGroupName=Playerbots
OutputDir=output
OutputBaseFilename=Playerbots_Setup
Compression=lzma2/ultra64
SolidCompression=yes
ArchitecturesInstallIn64BitMode=x64compatible
DisableProgramGroupPage=yes
DisableReadyPage=yes
UsePreviousTasks=yes

[Files]
Source: "dist\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Dirs]
Name: "{app}\logs"

[Tasks]
Name: "downloaddata"; Description: "Download client data (maps, vmaps, mmaps, dbc)"; GroupDescription: "Client Data:"; Flags: checkedonce
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Icons]
Name: "{group}\Playerbots Server Launcher"; Filename: "{app}\startup.exe"; WorkingDir: "{app}"
Name: "{commondesktop}\Playerbots Server Launcher"; Filename: "{app}\startup.exe"; WorkingDir: "{app}"; Tasks: desktopicon

[Run]
Filename: "{app}\startup.exe"; Description: "Launch Playerbots Server"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
Type: filesandordirs; Name: "{app}\data"

[Code]
var
  DownloadPage: TDownloadWizardPage;

procedure InitializeWizard;
begin
  DownloadPage := CreateDownloadPage(SetupMessage(msgWizardPreparing), SetupMessage(msgPreparingDesc), nil);
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ZipPath, DestDir: string;
  ResultCode: Integer;
begin
  // ssInstall runs immediately BEFORE files from dist\* are copied to {app}
  if CurStep = ssInstall then
  begin
    if WizardIsTaskSelected('downloaddata') then
    begin
      ZipPath := ExpandConstant('{tmp}\Data.zip');
      DestDir := ExpandConstant('{app}\data');

      // 1. Download Data.zip
      DownloadPage.Clear;
      DownloadPage.Add('https://github.com/wowgaming/client-data/releases/download/v20.0/Data.zip', 'Data.zip', '');
      DownloadPage.Show;
      try
        try
          DownloadPage.Download;
        except
          if DownloadPage.AbortedByUser then
            Log('Download aborted by user.')
          else
            SuppressibleMsgBox(AddPeriod(GetExceptionMessage), mbCriticalError, MB_OK, IDOK);
          Exit;
        end;
      finally
        DownloadPage.Hide;
      end;

      // 2. Extract Data.zip into {app}\data
      if FileExists(ZipPath) then
      begin
        WizardForm.StatusLabel.Caption := 'Extracting client data (this may take a few minutes)...';
        WizardForm.ProgressGauge.Style := npbstMarquee;
        ForceDirectories(DestDir);

        // Extract using Windows native tar.exe (fastest, built into Windows 10/11)
        if not Exec('tar.exe', Format('-xf "%s" -C "%s"', [ZipPath, DestDir]), '', SW_HIDE, ewWaitUntilTerminated, ResultCode) or (ResultCode <> 0) then
        begin
          // Fallback to PowerShell Expand-Archive if tar is unavailable
          Exec('powershell.exe', Format('-NoProfile -ExecutionPolicy Bypass -Command "Expand-Archive -LiteralPath ''%s'' -DestinationPath ''%s'' -Force"', [ZipPath, DestDir]), '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
        end;

        // Clean up temporary zip file
        DeleteFile(ZipPath);
      end;
    end;
  end;
end;
