Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"
!include "x64.nsh"
Var UpgradeMode
Var RegisteredDirectory
Var PayloadDirectory
!ifdef ARM64_PACKAGE
 !define APP_ENTRY "WebDAVMount-ARM64"
 !define APP_DISPLAY "WebDAVMount ARM64"
 !define PRODUCT_ID "WebDAVMount-ARM64-8AC56D6F-43A6-4E37-A0D0-36DF969B1648"
!else
 !define APP_ENTRY "WebDAVMount"
 !define APP_DISPLAY "WebDAVMount"
 !define PRODUCT_ID "WebDAVMount-8AC56D6F-43A6-4E37-A0D0-36DF969B1648"
!endif
Name "${APP_DISPLAY} ${VERSION}"
OutFile "${OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\${APP_ENTRY}"
RequestExecutionLevel user
SetCompressor zlib
!ifdef REPRODUCIBLE_FILES
 SetDateSave off
!endif
ShowInstDetails show
ShowUninstDetails show
!macro ShortcutPaths
 !ifdef TEST_KEY
  StrCpy $R8 "$INSTDIR\test-shortcuts\start\WebDAVMount.lnk"
  StrCpy $R9 "$INSTDIR\test-shortcuts\WebDAVMount.lnk"
 !else
  StrCpy $R8 "$SMPROGRAMS\${APP_ENTRY}\${APP_DISPLAY}.lnk"
  StrCpy $R9 "$DESKTOP\${APP_DISPLAY}.lnk"
 !endif
!macroend
!ifdef TEST_KEY
 !define REGKEY "Software\WebDAVMount\Tests\${TEST_KEY}"
 !define RUNKEY "Software\WebDAVMount\Tests\${TEST_KEY}-Run"
!else
 !define REGKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_ENTRY}"
 !define RUNKEY "Software\Microsoft\Windows\CurrentVersion\Run"
!endif
!define MUI_ICON "app.ico"
!define MUI_UNICON "app.ico"
!define MUI_WELCOMEPAGE_TITLE "安装 WebDAVMount"
!define MUI_WELCOMEPAGE_TEXT "只为当前 Windows 用户安装，不需要管理员权限。$\r$\n$\r$\n首次安装请选择空目录；检测到已有安装时，在原位置升级并保留连接、缓存、快捷方式和登录启动设置。$\r$\n$\r$\n升级前请在客户端点击“退出并卸载”。安装器不会覆盖便携目录，也不自动安装 WinFsp 或 WebView2。"
!insertmacro MUI_PAGE_WELCOME
!define MUI_PAGE_CUSTOMFUNCTION_PRE SkipUpgradePage
!insertmacro MUI_PAGE_COMPONENTS
!define MUI_PAGE_CUSTOMFUNCTION_PRE SkipUpgradePage
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_TEXT "安装完成。请从开始菜单或安装目录启动程序。$\r$\n$\r$\n首次使用需具备 WinFsp 和 WebView2。此开发预览尚未数字签名。"
!insertmacro MUI_PAGE_FINISH
!define MUI_UNCONFIRMPAGE_TEXT_TOP "卸载前请在程序中点击“退出并卸载”。$\r$\n$\r$\n仅移除程序文件和本安装创建的快捷方式；连接配置、缓存和额外文件保留。"
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=2052 "ProductName" "WebDAVMount"
VIAddVersionKey /LANG=2052 "FileDescription" "WebDAVMount 当前用户安装程序"
VIAddVersionKey /LANG=2052 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=2052 "LegalCopyright" "WebDAVMount contributors"

!macro PathCheck PREFIX
Function ${PREFIX}CheckPath
 Exch $R0
 Push $R1
 Push $R2
 path_loop_${PREFIX}:
 System::Call 'kernel32::GetFileAttributesW(w R0)i.R1'
 ${If} $R1 != -1
  IntOp $R2 $R1 & 0x400
  ${If} $R2 != 0
   MessageBox MB_OK|MB_ICONSTOP "路径包含目录联接或符号链接，已停止操作。" /SD IDOK
   SetErrorLevel 4
   Quit
  ${EndIf}
 ${EndIf}
 ${GetParent} "$R0" $R1
 StrCmp $R1 "" path_done_${PREFIX}
 StrCmp $R1 $R0 path_done_${PREFIX}
 StrCpy $R0 $R1
 Goto path_loop_${PREFIX}
 path_done_${PREFIX}:
 Pop $R2
 Pop $R1
 Pop $R0
FunctionEnd
!macroend
!insertmacro PathCheck ""
!insertmacro PathCheck "un."

Function un.CheckFileLock
 Exch $0
 Push $1
 ${If} ${FileExists} "$0"
  System::Call 'kernel32::CreateFileW(w r0,i 0x40000000,i 0,p 0,i 3,i 0,p 0)p.r1'
  ${If} $1 == -1
   !ifdef TEST_KEY
    System::Call 'kernel32::GetLastError()i.r2'
    FileOpen $3 "$INSTDIR\test-lock-failure.txt" w
    FileWrite $3 "$0 : $2"
    FileClose $3
   !endif
   MessageBox MB_OK|MB_ICONSTOP "程序仍在运行或程序文件被占用。请点击“退出并卸载”后重试。" /SD IDOK
   SetErrorLevel 5
   Quit
  ${EndIf}
  System::Call 'kernel32::CloseHandle(p r1)'
 ${EndIf}
 Pop $1
 Pop $0
FunctionEnd

Function .onInit
 SetShellVarContext current
 !ifdef ARM64_PACKAGE
 ${IfNot} ${IsNativeARM64}
  MessageBox MB_OK "此安装包需要 Windows ARM64。" /SD IDOK
  SetErrorLevel 2
  Quit
 ${EndIf}
 !endif
 SetRegView 64
 ; Automatic update handoff: capture a process handle, then wait for the
 ; client (and its ordinary Job-contained engine) to release installed files.
 ${GetParameters} $0
 ClearErrors
 ${GetOptions} $0 "/WAITPID=" $1
 ${IfNot} ${Errors}
  StrCmp $1 "" wait_client_invalid
  StrCpy $2 0
  wait_pid_digits:
   StrCpy $3 $1 1 $2
   StrCmp $3 "" wait_pid_valid
   StrCmp $3 "0" wait_pid_next
   StrCmp $3 "1" wait_pid_next
   StrCmp $3 "2" wait_pid_next
   StrCmp $3 "3" wait_pid_next
   StrCmp $3 "4" wait_pid_next
   StrCmp $3 "5" wait_pid_next
   StrCmp $3 "6" wait_pid_next
   StrCmp $3 "7" wait_pid_next
   StrCmp $3 "8" wait_pid_next
   StrCmp $3 "9" wait_pid_next wait_client_invalid
  wait_pid_next:
   IntOp $2 $2 + 1
   IntCmp $2 10 wait_pid_digits wait_pid_digits wait_client_invalid
  wait_pid_valid:
  IntCmp $1 0 wait_client_invalid wait_client_invalid
  System::Call 'kernel32::OpenProcess(i 0x100000,i 0,i r1)p.r2?e'
  Pop $3
  ${If} $2 != 0
   System::Call 'kernel32::WaitForSingleObject(p r2,i 30000)i.r3'
   System::Call 'kernel32::CloseHandle(p r2)'
   ${If} $3 != 0
    MessageBox MB_OK|MB_ICONSTOP "客户端尚未完全退出，请先退出并停止传输后重新运行安装包。" /SD IDOK
    SetErrorLevel 5
    Quit
   ${EndIf}
  ${Else}
   ${If} $3 != 87
    MessageBox MB_OK|MB_ICONSTOP "无法确认客户端退出状态，请手动退出后重新运行安装包。" /SD IDOK
    SetErrorLevel 5
    Quit
   ${EndIf}
  ${EndIf}
  Goto wait_client_done
  wait_client_invalid:
   SetErrorLevel 5
   Quit
  wait_client_done:
 ${EndIf}
 !ifndef ARM64_PACKAGE
 ${IfNot} ${RunningX64}
  MessageBox MB_OK "需要 Windows x64。" /SD IDOK
  SetErrorLevel 2
  Quit
 ${EndIf}
 !endif
 ReadRegStr $0 HKCU "${REGKEY}" "InstallLocation"
 StrCpy $UpgradeMode 0
 ${If} $0 != ""
  StrCpy $RegisteredDirectory $0
  StrCpy $INSTDIR $0
  StrCpy $UpgradeMode 1
  MessageBox MB_OKCANCEL|MB_ICONINFORMATION "将升级此安装：$\r$\n$INSTDIR$\r$\n$\r$\n请先点击客户端的“退出并卸载”。连接、缓存和额外文件保留；程序文件会校验并备份。快捷方式和登录启动设置保持原状。$\r$\n$\r$\n继续升级？" /SD IDOK IDOK upgrade_accepted
  SetErrorLevel 1
  Quit
  upgrade_accepted:
 ${EndIf}
 Call ChooseDesktopShortcut
FunctionEnd

Function SkipUpgradePage
 ${If} $UpgradeMode == 1
  Abort
 ${EndIf}
FunctionEnd

Function .onVerifyInstDir
 StrLen $0 $INSTDIR
 ${If} $0 < 4
  Abort
 ${EndIf}
 StrCpy $0 $INSTDIR 1 1
 ${If} $0 != ":"
  Abort
 ${EndIf}
FunctionEnd

Section "程序文件（必选）" Main
 SectionIn RO
 ${If} $UpgradeMode == 1
  StrCpy $INSTDIR $RegisteredDirectory
 ${EndIf}
 Call .onVerifyInstDir
 Push "$INSTDIR"
 Call CheckPath
 ${If} $UpgradeMode == 1
  InitPluginsDir
  StrCpy $PayloadDirectory "$PLUGINSDIR\payload"
  CreateDirectory "$PayloadDirectory"
  Goto empty_done_upgrade
 ${EndIf}
 FindFirst $0 $1 "$INSTDIR\*.*"
 check_empty:
 StrCmp $1 "" empty_done
 StrCmp $1 "." next_entry
 StrCmp $1 ".." next_entry
 FindClose $0
 MessageBox MB_OK|MB_ICONSTOP "安装目录必须为空，请选择新的空目录。" /SD IDOK
 SetErrorLevel 4
 Quit
 next_entry:
 FindNext $0 $1
 Goto check_empty
 empty_done:
 FindClose $0
 StrCpy $PayloadDirectory $INSTDIR
 empty_done_upgrade:
 ClearErrors
 SetOverwrite off
 !include "${FILE_LIST}"
 IfErrors 0 files_ok
 MessageBox MB_OK|MB_ICONSTOP "程序文件写入失败，请检查权限或磁盘空间。尚未登记安装信息。" /SD IDOK
 SetErrorLevel 6
 Quit
 files_ok:
 FileOpen $0 "$PayloadDirectory\WebDAVMount.install" w
 FileWrite $0 "${PRODUCT_ID}"
 FileClose $0
 WriteUninstaller "$PayloadDirectory\uninstall.exe"
 IfErrors 0 registration_ok
 MessageBox MB_OK|MB_ICONSTOP "安装记录写入失败，请检查目录权限及磁盘空间。" /SD IDOK
 SetErrorLevel 6
 Quit
 registration_ok:
 ${If} $UpgradeMode == 1
  SetOutPath "$PayloadDirectory"
  !ifdef TEST_UPGRADE_SCRIPT
   !ifndef TEST_KEY
    !error "An instrumented upgrade requires an isolated test registry key"
   !endif
   File "/oname=Upgrade-Installed.ps1" "${TEST_UPGRADE_SCRIPT}"
  !else
   File "Upgrade-Installed.ps1"
  !endif
  ClearErrors
  # Sysnative selects native Windows PowerShell from this x86 installer.
  ExecWait '"$WINDIR\Sysnative\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "$PayloadDirectory\Upgrade-Installed.ps1" -Source "$PayloadDirectory" -Target "$INSTDIR" -Version "${VERSION}" -RegistryKey "${REGKEY}"' $0
  ${If} ${Errors}
   StrCpy $0 7
  ${EndIf}
  ${If} $0 != 0
   !ifdef TEST_KEY
    CopyFiles /SILENT "$PayloadDirectory\upgrade-error.txt" "$INSTDIR\test-upgrade-error.txt"
    CopyFiles /SILENT "$PayloadDirectory\upgrade-debug.txt" "$INSTDIR\test-upgrade-debug.txt"
   !endif
   StrCpy $1 "升级未完成。原程序文件如已写入会尝试回退；请保留安装目录中的备份。"
   FileOpen $2 "$PayloadDirectory\upgrade-error.txt" r
   ${IfNot} ${Errors}
    FileReadUTF16LE $2 $1
    FileClose $2
   ${EndIf}
   MessageBox MB_OK|MB_ICONSTOP "升级已停止：$\r$\n$1$\r$\n$\r$\n请检查程序是否退出、文件占用、权限及校验情况后重试。" /SD IDOK
   SetErrorLevel 7
   Quit
  ${EndIf}
  DetailPrint "升级完成，原文件备份保留在安装目录。"
  Goto main_done
 ${EndIf}
 WriteRegStr HKCU "${REGKEY}" "DisplayName" "${APP_DISPLAY}"
 WriteRegStr HKCU "${REGKEY}" "DisplayVersion" "${VERSION}"
 WriteRegStr HKCU "${REGKEY}" "InstallLocation" "$INSTDIR"
 WriteRegStr HKCU "${REGKEY}" "DisplayIcon" "$INSTDIR\WebDAVMount.exe"
 WriteRegStr HKCU "${REGKEY}" "UninstallString" '$\"$INSTDIR\uninstall.exe$\"'
 WriteRegStr HKCU "${REGKEY}" "QuietUninstallString" '$\"$INSTDIR\uninstall.exe$\" /S'
 WriteRegDWORD HKCU "${REGKEY}" "NoModify" 1
 WriteRegDWORD HKCU "${REGKEY}" "NoRepair" 1
 main_done:
SectionEnd


Section "开始菜单快捷方式" StartShortcut
 ${If} $UpgradeMode == 1
  Goto start_done
 ${EndIf}
 !insertmacro ShortcutPaths
 ${GetParent} "$R8" $0
 Push "$0"
 Call CheckPath
 IfFileExists "$R8" start_done
 CreateDirectory "$0"
 CreateShortcut "$R8" "$INSTDIR\WebDAVMount.exe" "" "$INSTDIR\WebDAVMount.exe" 0
 WriteRegStr HKCU "${REGKEY}" "StartShortcut" "$R8"
 start_done:
SectionEnd

Section /o "桌面快捷方式" DesktopShortcut
 ${If} $UpgradeMode == 1
  Goto desktop_done
 ${EndIf}
 !insertmacro ShortcutPaths
 ${GetParent} "$R9" $0
 !ifdef TEST_KEY
  CreateDirectory "$0"
 !endif
 Push "$0"
 Call CheckPath
 IfFileExists "$R9" desktop_done
 CreateShortcut "$R9" "$INSTDIR\WebDAVMount.exe" "" "$INSTDIR\WebDAVMount.exe" 0
 WriteRegStr HKCU "${REGKEY}" "DesktopShortcut" "$R9"
 desktop_done:
SectionEnd

Function ChooseDesktopShortcut
 ${GetParameters} $0
 ClearErrors
 ${GetOptions} $0 "/DESKTOP" $1
 ${IfNot} ${Errors}
  SectionSetFlags ${DesktopShortcut} 1
 ${EndIf}
FunctionEnd

Function un.onInit
 SetShellVarContext current
 SetRegView 64
 Push "$INSTDIR"
 Call un.CheckPath
 ReadRegStr $0 HKCU "${REGKEY}" "InstallLocation"
 StrCmp $0 $INSTDIR 0 invalid_uninstall
 FileOpen $0 "$INSTDIR\WebDAVMount.install" r
 FileRead $0 $1
 FileClose $0
 StrCmp $1 "${PRODUCT_ID}" 0 invalid_uninstall
 !insertmacro ShortcutPaths
 ReadRegStr $0 HKCU "${REGKEY}" "StartShortcut"
 ${If} $0 != ""
  StrCmp $0 $R8 0 invalid_uninstall
  Push "$0"
  Call un.CheckPath
 ${EndIf}
 ReadRegStr $0 HKCU "${REGKEY}" "DesktopShortcut"
 ${If} $0 != ""
  StrCmp $0 $R9 0 invalid_uninstall
  Push "$0"
  Call un.CheckPath
 ${EndIf}
 !include "${UNINSTALL_CHECKS}"
 Push "$INSTDIR\WebDAVMount.install"
 Call un.CheckPath
 Return
 invalid_uninstall:
 MessageBox MB_OK|MB_ICONSTOP "安装信息与目录不一致，停止卸载。" /SD IDOK
 SetErrorLevel 4
 Quit
FunctionEnd

Function un.DeleteError
 MessageBox MB_OK|MB_ICONSTOP "部分程序文件无法移除。安装登记已保留，请关闭占用程序后重试卸载。" /SD IDOK
 SetErrorLevel 6
 Quit
FunctionEnd

Section "Uninstall"
 !include "${DELETE_LIST}"
 ReadRegStr $0 HKCU "${REGKEY}" "StartShortcut"
 ${If} $0 != ""
  Delete "$0"
  ${GetParent} "$0" $1
  RMDir "$1"
 ${EndIf}
 ReadRegStr $0 HKCU "${REGKEY}" "DesktopShortcut"
 ${If} $0 != ""
  Delete "$0"
 ${EndIf}
 !ifdef TEST_KEY
  RMDir "$INSTDIR\test-shortcuts"
 !endif
 ReadRegStr $0 HKCU "${RUNKEY}" "${APP_ENTRY}"
 StrCmp $0 '$\"$INSTDIR\WebDAVMount.exe$\"' 0 +2
 DeleteRegValue HKCU "${RUNKEY}" "${APP_ENTRY}"
 Delete "$INSTDIR\WebDAVMount.install"
 Delete "$INSTDIR\uninstall.exe"
 DeleteRegKey HKCU "${REGKEY}"
 RMDir "$INSTDIR"
SectionEnd
