@echo off
echo [1/3] Building Frontend...
cd ../frontend
call npm run build

echo [2/3] Moving Frontend to Backend...
if exist "..\backend\dist" rd /s /q "..\backend\dist"
xcopy /s /e /i "dist" "..\backend\dist"

echo [3/3] Building Backend (AirChat.exe)...
cd ../backend
go build -ldflags="-s -w" -o ../AirChat.exe

echo Done! AirChat.exe created in project root.
pause
