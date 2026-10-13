# Installs the Quicksand agent as a Windows service. Run in an elevated
# PowerShell:
#
#   .\install-windows.ps1 -ServerKey "ed25519:..." [-Subnet 10.99.0.0/24]
#
# ServerKey is the output of "quicksand agent pubkey"; Subnet is the agent
# network and must match Quicksand's config.
param(
    [Parameter(Mandatory = $true)][string]$ServerKey,
    [string]$Subnet = "10.99.0.0/24",
    [int]$Port = 7070
)
$ErrorActionPreference = "Stop"

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$binDir = Join-Path $env:ProgramFiles "Quicksand"
$dataDir = Join-Path $env:ProgramData "Quicksand"
$exe = Join-Path $binDir "quicksand-agent.exe"
$config = Join-Path $dataDir "agent.json"

$serviceName = "QuicksandAgent"

# Stop a running agent first: Windows locks the executable while it runs.
$existing = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
if ($existing) {
    Stop-Service -Name $serviceName -Force
    $existing.WaitForStatus("Stopped", (New-TimeSpan -Seconds 30))
}

New-Item -ItemType Directory -Force -Path $binDir, $dataDir | Out-Null
Copy-Item -Force (Join-Path $PSScriptRoot "quicksand-agent-windows-$arch.exe") $exe

# Config and logs: SYSTEM and Administrators only.
icacls $dataDir /inheritance:r /grant:r "SYSTEM:(OI)(CI)F" "Administrators:(OI)(CI)F" | Out-Null
@{ authorizedKeys = @($ServerKey); port = $Port; network = @{ subnet = $Subnet } } |
    ConvertTo-Json | Set-Content -Encoding UTF8 $config

# Windows Firewall blocks inbound connections by default. The Proxmox VM
# firewall already limits who can reach this port to the Quicksand server.
Get-NetFirewallRule -DisplayName "Quicksand agent" -ErrorAction SilentlyContinue | Remove-NetFirewallRule
New-NetFirewallRule -DisplayName "Quicksand agent" -Direction Inbound -Action Allow `
    -Protocol TCP -LocalPort $Port -Program $exe -Profile Any | Out-Null

# A Windows service running as LocalSystem (needed to configure the agent
# interface), started automatically and restarted if it fails.
$binaryPath = "`"$exe`" -config `"$config`" -log `"$dataDir\agent.log`""
if ($existing) {
    sc.exe config $serviceName binPath= $binaryPath start= auto | Out-Null
} else {
    New-Service -Name $serviceName -DisplayName "Quicksand agent" -BinaryPathName $binaryPath `
        -StartupType Automatic -Description "Quicksand guest agent (see docs/agent.md)." | Out-Null
}
sc.exe failure $serviceName reset= 86400 actions= restart/5000/restart/5000/restart/30000 | Out-Null
sc.exe failureflag $serviceName 1 | Out-Null
Start-Service -Name $serviceName
Write-Host "Installed the $serviceName service. Log: $dataDir\agent.log"
