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
; The new executable is written next to the old one and renamed over it,
; before anything in the registry changes: if the app cannot be closed or
; the file cannot be replaced, the installer exits with code 2, the old
; version stays as it was and, with /RELAUNCH, is started again (the
; in-app updater has already quit).
;
; Options: /S silent; /RELAUNCH start the app once installed (the in-app
; updater runs "/S /RELAUNCH", see internal/update; an interactive
; install offers it on the last page instead); /D=<dir> (standard).
; Needs 64-bit Windows 10 or 11.
;
; The uninstaller removes the program, the shortcut, the autostart value
; (only if it starts this copy), Wails' registration of the app for
; notifications (only if it points to this copy) and WebView2's cache
; (%APPDATA%\sd-sync-immich.exe), never the user's data:
; %APPDATA%\sd-sync-immich (settings, history) and
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
; WAIT_STEPS checks, 0.5 s apart (30 s). A silent uninstall waits only
; UN_WAIT_STEPS (2 s): nothing asked the app to quit.
!define WAIT_STEPS 60
!define UN_WAIT_STEPS 4
; Wails' notification service (v3.0.0-beta.34,
; pkg/services/notifications/notifications_windows.go) registers the app
; under its name: the AppUserModelId key holds a GUID ("CustomActivator")
; whose CLSID key starts this executable; its icon is
; %TEMP%\<name><GUID>.png and its categories are in
; HKCU\Software\<name>\NotificationCategories.
!define TOAST_AUMID_KEY "Software\Classes\AppUserModelId\${PRODUCT}"
!define TOAST_CLSID_ROOT "Software\Classes\CLSID"
!define TOAST_APP_KEY "Software\${PRODUCT}"

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"
!include "WordFunc.nsh"
!include "x64.nsh"
!include "WinVer.nsh"

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

LangString Needs64 ${LANG_ENGLISH} "${PRODUCT} needs 64-bit Windows 10 or 11."
LangString Needs64 ${LANG_RUSSIAN} "Для ${PRODUCT} нужна 64-разрядная Windows 10 или 11."
LangString Needs64 ${LANG_GERMAN} "${PRODUCT} benötigt 64-Bit-Windows 10 oder 11."
LangString Needs64 ${LANG_SPANISH} "${PRODUCT} necesita Windows 10 u 11 de 64 bits."
LangString Needs64 ${LANG_FRENCH} "${PRODUCT} nécessite Windows 10 ou 11 64 bits."
LangString Needs64 ${LANG_PORTUGUESEBR} "O ${PRODUCT} requer o Windows 10 ou 11 de 64 bits."
LangString Needs64 ${LANG_SIMPCHINESE} "${PRODUCT} 需要 64 位 Windows 10 或 11。"
LangString Needs64 ${LANG_JAPANESE} "${PRODUCT} には 64 ビット版の Windows 10 または 11 が必要です。"

LangString InstallFailed ${LANG_ENGLISH} "${PRODUCT} could not be updated: its file could not be replaced. Restart Windows and run the installer again."
LangString InstallFailed ${LANG_RUSSIAN} "Не удалось обновить ${PRODUCT}: файл программы не удалось заменить. Перезагрузите Windows и запустите установщик снова."
LangString InstallFailed ${LANG_GERMAN} "${PRODUCT} konnte nicht aktualisiert werden: Die Programmdatei ließ sich nicht ersetzen. Starten Sie Windows neu und führen Sie das Installationsprogramm erneut aus."
LangString InstallFailed ${LANG_SPANISH} "No se pudo actualizar ${PRODUCT}: no se pudo reemplazar su archivo. Reinicie Windows y vuelva a ejecutar el instalador."
LangString InstallFailed ${LANG_FRENCH} "Impossible de mettre à jour ${PRODUCT} : son fichier n'a pas pu être remplacé. Redémarrez Windows et relancez le programme d'installation."
LangString InstallFailed ${LANG_PORTUGUESEBR} "Não foi possível atualizar o ${PRODUCT}: o arquivo do programa não pôde ser substituído. Reinicie o Windows e execute o instalador novamente."
LangString InstallFailed ${LANG_SIMPCHINESE} "无法更新 ${PRODUCT}：无法替换其程序文件。请重新启动 Windows 后再次运行安装程序。"
LangString InstallFailed ${LANG_JAPANESE} "${PRODUCT} を更新できませんでした。プログラムのファイルを置き換えられませんでした。Windows を再起動して、もう一度インストーラーを実行してください。"

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
	${IfNot} ${RunningX64}
	${OrIfNot} ${AtLeastWin10}
		MessageBox MB_OK|MB_ICONSTOP "$(Needs64)" /SD IDOK
		SetErrorLevel 3
		Abort
	${EndIf}
	StrCpy $Relaunch 0
	${GetParameters} $R0
	ClearErrors
	${GetOptions} $R0 "/RELAUNCH" $R1
	${IfNot} ${Errors}
		StrCpy $Relaunch 1
	${EndIf}
FunctionEnd

; RelaunchOld starts the installed app again when the installer was
; asked to (/RELAUNCH) but leaves it as it was: the in-app updater has
; quit, and without this the user would be left without the app until the
; next login. If the old process still runs, the new one only shows its
; window (single-instance lock) and exits.
Function RelaunchOld
	${If} $Relaunch == 1
	${AndIf} ${FileExists} "$INSTDIR\${EXE}"
		Exec '"$INSTDIR\${EXE}"'
	${EndIf}
FunctionEnd

; InstallFailed ends an install that could not replace the executable,
; before anything in the registry has changed: exit code 2.
Function InstallFailed
	DetailPrint "Could not replace $INSTDIR\${EXE}"
	${IfNot} ${Silent}
		MessageBox MB_OK|MB_ICONSTOP "$(InstallFailed)"
	${EndIf}
	Call RelaunchOld
	SetErrorLevel 2
	Quit
FunctionEnd

; UserFilter, AppRunning, CloseApp and RunValueOurs, for the installer
; (prefix "") and the uninstaller (prefix "un."). waitSteps: how long a
; silent CloseApp waits for the app to quit (0.5 s each).
!macro APP_FUNCTIONS un waitSteps
	; Pushes the tasklist/taskkill filter for this user's processes, or ""
	; when %USERNAME% is not set (then any user's: Windows does not let a
	; user end another user's processes anyway). Uses only $R6.
	Function ${un}UserFilter
		ReadEnvStr $R6 USERNAME
		${If} $R6 == ""
			Push ""
		${Else}
			Push '/FI "USERNAME eq $R6"'
		${EndIf}
	FunctionEnd

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
		Call ${un}UserFilter
		Pop $R6
		nsExec::ExecToStack '"$SYSDIR\tasklist.exe" /NH /FO CSV /FI "IMAGENAME eq ${EXE}" $R6'
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

	; Returns once the app is not running. Silent: waits for it to quit
	; (the in-app updater quits right after starting the installer), then
	; ends it. Interactive: asks, then ends it. If it still runs, the
	; installer exits with code 2 and changes nothing (with /RELAUNCH it
	; starts the installed app again, see RelaunchOld).
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
			${LoopUntil} $1 >= ${waitSteps}
		${Else}
			MessageBox MB_OKCANCEL|MB_ICONINFORMATION "$(AppRunning)" IDOK closeApp
			SetErrorLevel 1
			Quit
		closeApp:
		${EndIf}

		DetailPrint "Closing ${PRODUCT}"
		Call ${un}UserFilter
		Pop $2
		nsExec::Exec '"$SYSDIR\taskkill.exe" /F /IM ${EXE} $2'
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
		!if "${un}" == ""
			Call RelaunchOld
		!endif
		SetErrorLevel 2
		Quit
	FunctionEnd

	; Pushes 1 when the autostart value starts a program in $INSTDIR (the
	; path at its start, quoted or not, as internal/install reads it),
	; else 0: a value for a copy elsewhere is not this install's. Uses only
	; $R6-$R9.
	Function ${un}RunValueOurs
		ReadRegStr $R6 HKCU "${RUN_KEY}" "${RUN_VALUE}"
		StrCpy $R7 $R6 1
		${If} $R7 == '"'
			StrCpy $R6 $R6 "" 1
		${EndIf}
		StrCpy $R9 "$INSTDIR\"
		StrLen $R8 $R9
		StrCpy $R7 $R6 $R8
		; StrCmp, behind ==, ignores case, as Windows paths do.
		${If} $R6 != ""
		${AndIf} $R7 == $R9
			Push 1
		${Else}
			Push 0
		${EndIf}
	FunctionEnd
!macroend

!insertmacro APP_FUNCTIONS "" ${WAIT_STEPS}
!insertmacro APP_FUNCTIONS "un." ${UN_WAIT_STEPS}

Section "Install"
	SetShellVarContext current
	Call CloseApp

	; Read before this install writes it: no uninstall entry means a first
	; install.
	ReadRegStr $2 HKCU "${UNINST_KEY}" "UninstallString"

	; The new executable next to the old one, then renamed over it; on any
	; failure the old one stays (InstallFailed). "try": no retry dialog,
	; only the error flag.
	SetOutPath "$INSTDIR"
	Delete "$INSTDIR\${EXE}.new"
	Delete "$INSTDIR\${EXE}.old"
	SetOverwrite try
	ClearErrors
	File "/oname=${EXE}.new" "${APP_EXE}"
	SetOverwrite on
	${If} ${Errors}
		Delete "$INSTDIR\${EXE}.new"
		Call InstallFailed
	${EndIf}
	${If} ${FileExists} "$INSTDIR\${EXE}"
		ClearErrors
		Rename "$INSTDIR\${EXE}" "$INSTDIR\${EXE}.old"
		${If} ${Errors}
			Delete "$INSTDIR\${EXE}.new"
			Call InstallFailed
		${EndIf}
	${EndIf}
	ClearErrors
	Rename "$INSTDIR\${EXE}.new" "$INSTDIR\${EXE}"
	${If} ${Errors}
		Rename "$INSTDIR\${EXE}.old" "$INSTDIR\${EXE}"
		Delete "$INSTDIR\${EXE}.new"
		Call InstallFailed
	${EndIf}
	Delete "$INSTDIR\${EXE}.old"

	WriteUninstaller "$INSTDIR\${UNINSTALLER}"
	CreateShortcut "$SMPROGRAMS\${PRODUCT}.lnk" "$INSTDIR\${EXE}"

	; Start at login: on the first install, and on an update when it is on
	; for this copy (rewritten in today's form). A value for a copy
	; elsewhere, or none (turned off), is left alone.
	${If} $2 == ""
		WriteRegStr HKCU "${RUN_KEY}" "${RUN_VALUE}" '"$INSTDIR\${EXE}" run --autostart'
	${Else}
		Call RunValueOurs
		Pop $3
		${If} $3 == 1
			WriteRegStr HKCU "${RUN_KEY}" "${RUN_VALUE}" '"$INSTDIR\${EXE}" run --autostart'
		${EndIf}
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
	; single-instance lock. No arguments: it runs. Only silent: an
	; interactive install offers it on the finish page, which would start
	; it a second time.
	${If} $Relaunch == 1
	${AndIf} ${Silent}
		Exec '"$INSTDIR\${EXE}"'
	${EndIf}
SectionEnd

Section "Uninstall"
	SetShellVarContext current
	; The app is 64-bit: its notification registration is in the 64-bit
	; view of HKCU\Software\Classes\CLSID (the other keys are shared).
	SetRegView 64
	Call un.CloseApp

	Call un.RunValueOurs
	Pop $0
	${If} $0 == 1
		DeleteRegValue HKCU "${RUN_KEY}" "${RUN_VALUE}"
	${EndIf}

	; Wails' notification registration, if it starts this copy. The GUID
	; must look like one ({...}, 38 characters): an empty one would name
	; the whole CLSID key.
	ReadRegStr $0 HKCU "${TOAST_AUMID_KEY}" "CustomActivator"
	StrLen $1 $0
	StrCpy $2 $0 1
	StrCpy $3 $0 1 -1
	${If} $1 == 38
	${AndIf} $2 == "{"
	${AndIf} $3 == "}"
		ReadRegStr $1 HKCU "${TOAST_CLSID_ROOT}\$0\LocalServer32" ""
		StrCpy $2 '"$INSTDIR\${EXE}"'
		StrLen $3 $2
		StrCpy $3 $1 $3
		${If} $1 == ""
		${OrIf} $3 == $2
			DeleteRegKey HKCU "${TOAST_CLSID_ROOT}\$0"
			DeleteRegKey HKCU "${TOAST_AUMID_KEY}"
			DeleteRegKey HKCU "${TOAST_APP_KEY}\NotificationCategories"
			DeleteRegKey /ifempty HKCU "${TOAST_APP_KEY}"
			Delete "$TEMP\${PRODUCT}$0.png"
		${EndIf}
	${EndIf}

	Delete "$SMPROGRAMS\${PRODUCT}.lnk"
	Delete "$INSTDIR\${EXE}"
	Delete "$INSTDIR\${EXE}.new"
	Delete "$INSTDIR\${EXE}.old"
	Delete "$INSTDIR\${UNINSTALLER}"
	; Only if empty: the user may have put other files there.
	RMDir "$INSTDIR"
	; WebView2's cache (Wails' default user data folder, named after the
	; executable); the app's own data is in %APPDATA%\sd-sync-immich.
	RMDir /r "$APPDATA\${EXE}"
	DeleteRegKey HKCU "${UNINST_KEY}"
SectionEnd
