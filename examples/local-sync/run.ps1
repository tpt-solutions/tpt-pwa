# Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
# One-command local-sync demo (examples/local-sync/README.md).
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..\..")

Write-Host "== building cortex-engine =="
Push-Location cortex-engine
cargo build | Out-Null
Pop-Location
$Engine = "cortex-engine\target\debug\cortex-engine.exe"

$procs = @()
try {
    Write-Host "== mock sync endpoint :9999 =="
    $procs += Start-Process -PassThru -WorkingDirectory cortex-daemon go -ArgumentList "run","./cmd/cortex-demo","serve-mock","-addr","127.0.0.1:9999" -NoNewWindow
    Start-Sleep -Seconds 1

    Write-Host "== daemon (engine-backed) =="
    $procs += Start-Process -PassThru -WorkingDirectory cortex-daemon go -ArgumentList "run","./cmd/cortex-daemon","-engine",$Engine,"-sync-endpoint","http://127.0.0.1:9999/sync" -NoNewWindow
    Start-Sleep -Seconds 2

    Write-Host "== enqueue one note =="
    Push-Location cortex-daemon; go run ./cmd/cortex-demo sync-once; Pop-Location -endpoint http://127.0.0.1:9999/sync

    Write-Host "== done =="
}
finally {
    foreach ($p in $procs) {
        try { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue } catch {}
    }
}
