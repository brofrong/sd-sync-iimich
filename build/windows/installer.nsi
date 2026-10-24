; SD Sync Immich for Windows: a per-user installer (no administrator
; rights). scripts/build-windows.ps1 builds it:
;
;   makensis -INPUTCHARSET UTF8 -DVERSION=0.5.0 -DAPP_EXE=<sd-sync-immich.exe>
;            -DOUTFILE=<SD-Sync-Immich-Setup-x64.exe> build\windows\installer.nsi
;
; VERSION is MAJOR.MINOR.PATCH (digits only); paths may be relative to this
; directory, where makensis runs the script.
;
; It installs into %LOCALAPPDATA%\Programs\SD Sync Immich, adds a Start menu
; shortcut (no arguments: the app runs), an entry in "Installed apps" and,
; on the first install, the value that starts the app at login, the same
; one the app's Settings write (internal/install). An update keeps that
; value only if it is there: turning autostart off survives updates.
;
; Before copying it waits for the running app to exit: the executable is
; locked while it runs, and the app holds a single-instance mutex, so a
; new copy started next to it would only show the old one's window and
; exit. Silent (/S, the in-app updater): it waits up to 30 s, the app is
; quitting on its own, then ends it. Interactive: it asks, then ends it.
;
; Options: /S silent; /RELAUNCH start the app once installed (the in-app
; updater runs "/S /RELAUNCH", see internal/update); /D=<dir> (standard).
;
; The uninstaller removes the program, the shortcut, the autostart value
; and WebView2's cache (%APPDATA%\sd-sync-immich.exe), never the user's
; data: %APPDATA%\sd-sync-immich (settings, history) and
; %LOCALAPPDATA%\sd-sync-immich (logs).

Unicode true
ManifestDPIAware true
RequestExecutionLevel user
SetCompressor /SOLID lzma

!ifndef VERSION
	!error "define VERSION (MAJOR.MINOR.PATCH), e.g. -DVERSION=0.5.0"
!endif
!ifndef APP_EXE
	!error "define APP_EXE, the path to sd-sync-immich.exe"
!endif
!ifndef OUTFILE
	!error "define OUTFILE, the path of the installer to write"
!endif

!define PRODUCT "SD Sync Immich"
!define EXE "sd-sync-immich.exe"
!define UNINSTALLER "uninstall.exe"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT}"
; The value the app's Settings write and remove (internal/install).
!define RUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"
!define RUN_VALUE "SD Sync Immich"
!define REPO_URL "https://github.com/brofrong/sd-sync-iimich"
; How long a silent install waits for the app to quit before ending it:
; WAIT_STEPS checks, 0.5 s apart (30 s).
!define WAIT_STEPS 60

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"
!include "WordFunc.nsh"

Name "${PRODUCT}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\${PRODUCT}"
; An update installs where the previous version is.
InstallDirRegKey HKCU "${UNINST_KEY}" "InstallLocation"
ShowInstDetails show
ShowUninstDetails show

!define MUI_ICON "icon.ico"
!define MUI_UNICON "icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXE}"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

; The installer follows the Windows display language; English (the first)
; otherwise. The same eight languages as the app.
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "Russian"
!insertmacro MUI_LANGUAGE "German"
!insertmacro MUI_LANGUAGE "Spanish"
!insertmacro MUI_LANGUAGE "French"
!insertmacro MUI_LANGUAGE "PortugueseBR"
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "Japanese"

LangString AppRunning ${LANG_ENGLISH} "${PRODUCT} is running. It will be closed to continue."
LangString AppRunning ${LANG_RUSSIAN} "${PRODUCT} запущено. Чтобы продолжить, оно будет закрыто."
LangString AppRunning ${LANG_GERMAN} "${PRODUCT} wird ausgeführt. Zum Fortfahren wird es beendet."
LangString AppRunning ${LANG_SPANISH} "${PRODUCT} se está ejecutando. Se cerrará para continuar."
LangString AppRunning ${LANG_FRENCH} "${PRODUCT} est en cours d'exécution. Il sera fermé pour continuer."
LangString AppRunning ${LANG_PORTUGUESEBR} "O ${PRODUCT} está em execução. Ele será fechado para continuar."
LangString AppRunning ${LANG_SIMPCHINESE} "${PRODUCT} 正在运行。将关闭它以继续。"
LangString AppRunning ${LANG_JAPANESE} "${PRODUCT} は実行中です。続行するために終了します。"

LangString AppStillRunning ${LANG_ENGLISH} "${PRODUCT} could not be closed. Quit it from the tray menu and try again."
LangString AppStillRunning ${LANG_RUSSIAN} "Не удалось закрыть ${PRODUCT}. Выйдите из него через меню в трее и попробуйте снова."
LangString AppStillRunning ${LANG_GERMAN} "${PRODUCT} konnte nicht beendet werden. Beenden Sie es über das Menü im Infobereich und versuchen Sie es erneut."
LangString AppStillRunning ${LANG_SPANISH} "No se pudo cerrar ${PRODUCT}. Ciérrelo desde el menú de la bandeja del sistema e inténtelo de nuevo."
LangString AppStillRunning ${LANG_FRENCH} "Impossible de fermer ${PRODUCT}. Quittez-le depuis le menu de la zone de notification et réessayez."
LangString AppStillRunning ${LANG_PORTUGUESEBR} "Não foi possível fechar o ${PRODUCT}. Saia dele pelo menu da bandeja do sistema e tente novamente."
LangString AppStillRunning ${LANG_SIMPCHINESE} "无法关闭 ${PRODUCT}。请从系统托盘菜单退出后重试。"
LangString AppStillRunning ${LANG_JAPANESE} "${PRODUCT} を終了できませんでした。タスクトレイのメニューから終了して、もう一度お試しください。"

VIProductVersion "${VERSION}.0"
VIFileVersion "${VERSION}.0"
VIAddVersionKey /LANG=${LANG_ENGLISH} "ProductName" "${PRODUCT}"
VIAddVersionKey /LANG=${LANG_ENGLISH} "FileDescription" "${PRODUCT} Setup"
VIAddVersionKey /LANG=${LANG_ENGLISH} "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=${LANG_ENGLISH} "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=${LANG_ENGLISH} "LegalCopyright" "MIT License"

; 1 when started with /RELAUNCH.
Var Relaunch

Function .onInit
	StrCpy $Relaunch 0
	${GetParameters} $R0
	ClearErrors
	${GetOptions} $R0 "/RELAUNCH" $R1
	${IfNot} ${Errors}
		StrCpy $Relaunch 1
	${EndIf}
FunctionEnd

; AppRunning and CloseApp, for the installer (prefix "") and the
; uninstaller (prefix "un.").
!macro APP_FUNCTIONS un
	; Pushes 1 while sd-sync-immich.exe runs for this user, else 0: the
	; installed copy (Windows does not let a running program's file be
	; opened for writing) or one elsewhere (tasklist lists it; its "none
	; found" message is localised, so only a line with the name counts).
	; Uses only $R6-$R9: CloseApp keeps its counter in $1.
	Function ${un}AppRunning
		${If} ${FileExists} "$INSTDIR\${EXE}"
			ClearErrors
			FileOpen $R6 "$INSTDIR\${EXE}" a
			${If} ${Errors}
				Push 1
				Return
			${EndIf}
			FileClose $R6
		${EndIf}
		ReadEnvStr $R6 USERNAME
		nsExec::ExecToStack '"$SYSDIR\tasklist.exe" /NH /FO CSV /FI "IMAGENAME eq ${EXE}" /FI "USERNAME eq $R6"'
		Pop $R7
		Pop $R8
		${If} $R7 == 0
			ClearErrors
			${WordFind} "$R8" '"${EXE}"' "E*" $R9
			${IfNot} ${Errors}
				Push 1
				Return
			${EndIf}
		${EndIf}
		Push 0
	FunctionEnd

	; Returns once the app is not running. Silent: waits up to 30 s for it
	; to quit (the in-app updater quits right after starting the
	; installer), then ends it. Interactive: asks, then ends it. If it
	; still runs, the installer exits with code 2 and changes nothing.
	Function ${un}CloseApp
		Call ${un}AppRunning
		Pop $0
		${If} $0 == 0
			Return
		${EndIf}

		${If} ${Silent}
			DetailPrint "Waiting for ${PRODUCT} to quit"
			StrCpy $1 0
			${Do}
				Sleep 500
				Call ${un}AppRunning
				Pop $0
				${If} $0 == 0
					Return
				${EndIf}
				IntOp $1 $1 + 1
			${LoopUntil} $1 >= ${WAIT_STEPS}
		${Else}
			MessageBox MB_OKCANCEL|MB_ICONINFORMATION "$(AppRunning)" IDOK closeApp
			SetErrorLevel 1
			Quit
		closeApp:
		${EndIf}

		DetailPrint "Closing ${PRODUCT}"
		ReadEnvStr $0 USERNAME
		nsExec::Exec '"$SYSDIR\taskkill.exe" /F /IM ${EXE} /FI "USERNAME eq $0"'
		Pop $0
		StrCpy $1 0
		${Do}
			Call ${un}AppRunning
			Pop $0
			${If} $0 == 0
				Return
			${EndIf}
			Sleep 500
			IntOp $1 $1 + 1
		${LoopUntil} $1 >= 20

		DetailPrint "${PRODUCT} is still running"
		${IfNot} ${Silent}
			MessageBox MB_OK|MB_ICONSTOP "$(AppStillRunning)"
		${EndIf}
		SetErrorLevel 2
		Quit
	FunctionEnd
!macroend

!insertmacro APP_FUNCTIONS ""
!insertmacro APP_FUNCTIONS "un."

Section "Install"
	SetShellVarContext current
	Call CloseApp

	; Read before this install writes them: no uninstall entry means a
	; first install.
	ReadRegStr $2 HKCU "${UNINST_KEY}" "UninstallString"
	ReadRegStr $3 HKCU "${RUN_KEY}" "${RUN_VALUE}"

	SetOutPath "$INSTDIR"
	File "/oname=${EXE}" "${APP_EXE}"
	WriteUninstaller "$INSTDIR\${UNINSTALLER}"

	CreateShortcut "$SMPROGRAMS\${PRODUCT}.lnk" "$INSTDIR\${EXE}"

	; Start at login: on the first install, and on an update when it is
	; on (rewritten, so it starts this copy).
	${If} $2 == ""
	${OrIf} $3 != ""
		WriteRegStr HKCU "${RUN_KEY}" "${RUN_VALUE}" '"$INSTDIR\${EXE}" run --autostart'
	${EndIf}

	WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${PRODUCT}"
	WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
	WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${EXE}"
	WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "brofrong"
	WriteRegStr HKCU "${UNINST_KEY}" "URLInfoAbout" "${REPO_URL}"
	WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
	WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\${UNINSTALLER}"'
	WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\${UNINSTALLER}" /S'
	WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
	WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
	${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
	IntFmt $0 "0x%08X" $0
	WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"

	; The old version is gone (CloseApp), so the new one does not find its
	; single-instance lock. No arguments: it runs.
	${If} $Relaunch == 1
		Exec '"$INSTDIR\${EXE}"'
	${EndIf}
SectionEnd

Section "Uninstall"
	SetShellVarContext current
	Call un.CloseApp

	DeleteRegValue HKCU "${RUN_KEY}" "${RUN_VALUE}"
	Delete "$SMPROGRAMS\${PRODUCT}.lnk"
	Delete "$INSTDIR\${EXE}"
	Delete "$INSTDIR\${UNINSTALLER}"
	; Only if empty: the user may have put other files there.
	RMDir "$INSTDIR"
	; WebView2's cache (Wails' default user data folder, named after the
	; executable); the app's own data is in %APPDATA%\sd-sync-immich.
	RMDir /r "$APPDATA\${EXE}"
	DeleteRegKey HKCU "${UNINST_KEY}"
SectionEnd
