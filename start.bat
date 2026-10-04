@echo off
chcp 65001 >nul
title AI Proxy 本地负载均衡服务

cd /d "%~dp0"

echo ======================================================
echo              AI Proxy 本地轮询代理服务
echo ======================================================

if not exist "config.yaml" (
    if exist "config.example.yaml" (
        echo [提示] 检测到未创建 config.yaml，正在从 config.example.yaml 复制...
        copy /y "config.example.yaml" "config.yaml" >nul
        echo [成功] 已生成 config.yaml，请用文本编辑器填入你的 OpenAI BaseURL 和 API Key！
    ) else (
        echo [错误] 未找到 config.yaml 或 config.example.yaml，请检查文件完整性。
        pause
        exit /b 1
    )
)

if not exist "ai-proxy.exe" (
    echo [错误] 当前目录下未找到 ai-proxy.exe。
    echo 本项目遵循“云端 GitHub 构建”规范，请从 GitHub 仓库的 Releases 或 Actions 中下载编译好的 ai-proxy.exe 放于本目录。
    echo.
    pause
    exit /b 1
)

echo [启动中] 正在启动本地代理...
ai-proxy.exe
if errorlevel 1 (
    echo.
    echo [异常] 代理进程已退出，按任意键关闭窗口...
    pause >nul
)
