@echo off
chcp 936 >nul
title MAGLAB 优化状态检查
echo ============================================
echo MAGLAB CS2 优化状态检查
echo ============================================
echo.
echo [1] 当前电源计划：
powercfg /getactivescheme
echo.
echo [2] Windows 游戏模式（1=开启）：
reg query "HKCU\Software\Microsoft\GameBar" /v AutoGameModeEnabled 2>nul
reg query "HKCU\Software\Microsoft\GameBar" /v AllowAutoGameMode 2>nul
echo.
echo [3] 硬件加速 GPU 调度（1=开启，2=关闭）：
reg query "HKLM\SYSTEM\CurrentControlSet\Control\GraphicsDrivers" /v HwSchMode 2>nul
echo.
echo [4] 快速启动（0=关闭，1=开启）：
reg query "HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Power" /v HiberbootEnabled 2>nul
echo.
echo [5] SysMain 服务状态：
sc query SysMain | findstr "STATE"
echo.
echo [6] Windows 搜索服务状态：
sc query WSearch | findstr "STATE"
echo.
echo [7] 最小处理器状态（当前电源计划，交流=插电）：
powercfg /query SCHEME_CURRENT 54533251-82be-4824-96c1-47b60b740d00 bc5038f7-23e0-4960-96da-33abaf5935ec | findstr /i "交流 AC"
echo.
echo [8] USB 选择性暂停（0=禁用，交流=插电）：
powercfg /query SCHEME_CURRENT 2a737441-1930-4402-8d77-b2bebba308a3 48e6b7a6-50f5-4782-a5d4-53bb8f07e226 | findstr /i "交流 AC"
echo.
echo [9] PCI Express 链路状态电源管理（0=关闭，交流=插电）：
powercfg /query SCHEME_CURRENT 501a4d13-42af-4429-9fd1-a8218c268e20 ee12f906-d277-404b-b6da-e5fa1a576df5 | findstr /i "交流 AC"
echo.
echo ============================================
echo 检查完成。按任意键退出。
pause >nul
