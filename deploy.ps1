<#
Install puck-tak on an MF688 puck over adb (Windows).

  .\deploy.ps1                       install or update, bind to 192.168.100.1
  .\deploy.ps1 -Bind 10.0.0.1        use a different WiFi address
  .\deploy.ps1 -NoAutostart          install and start, but don't touch /system
  .\deploy.ps1 -Uninstall            stop it and undo the auto-start changes (keeps packages)
  .\deploy.ps1 -Uninstall -Purge     ... and delete all data packages and the TLS cert

Easiest: double-click deploy.cmd, or run it from a Command Prompt with the same options.
Works in Windows PowerShell 5.1 and PowerShell 7.
#>
[CmdletBinding()]
param(
    [string]$Bind = "192.168.100.1",
    [switch]$NoAutostart,
    [switch]$Uninstall,
    [switch]$Purge
)
$ErrorActionPreference = "Stop"
Set-Location -LiteralPath $PSScriptRoot

$Bin      = Join-Path (Join-Path $PSScriptRoot "bin") "puck-tak-armv7"
$Dir      = "/data/local/tmp/tak"
$Wrap     = "/data/local/tmp/himiwebserver-wrapper.sh"
$Himi     = "/system/bin/himiwebserver"
$Prop     = "/data/local.prop"
$PropLine = "sys.himiweb_start=1"

function Say($m)  { Write-Host ""; Write-Host "==> $m" -ForegroundColor Cyan }
function Warn($m) { Write-Host "  ! $m" -ForegroundColor Yellow }
function Die($m)  { Write-Host ""; Write-Host "ERROR: $m" -ForegroundColor Red; exit 1 }

# find adb: PATH, a platform-tools folder next to this script, or the Android SDK
$Adb = $null
$cmd = Get-Command adb -ErrorAction SilentlyContinue
if ($cmd) { $Adb = $cmd.Source }
$candidates = @(Join-Path (Join-Path $PSScriptRoot "platform-tools") "adb.exe")
if ($env:LOCALAPPDATA) { $candidates += Join-Path $env:LOCALAPPDATA "Android\Sdk\platform-tools\adb.exe" }
foreach ($c in $candidates) {
    if (-not $Adb -and $c -and (Test-Path -LiteralPath $c)) { $Adb = $c }
}
if (-not $Adb) { Die "adb not found. Unzip Android platform-tools into a 'platform-tools' folder next to this script (see INSTALL.md)." }

# Run adb; adb writes progress to stderr, which must not count as an error here.
function AdbRun {
    $old = $ErrorActionPreference; $ErrorActionPreference = "Continue"
    try { $out = & $Adb @args 2>&1 | ForEach-Object { "$_" } } finally { $ErrorActionPreference = $old }
    return $out
}
# old adbd does not pass exit codes back, so every check reads output instead
function ShOut([string]$c) { ((AdbRun shell $c) -join "`n").Replace("`r", "").Trim() }
function Exists([string]$p) { (ShOut "if [ -e $p ]; then echo yes; else echo no; fi") -eq "yes" }
function StopServer {
    AdbRun shell "if [ -f $Dir/pid ]; then kill `$(cat $Dir/pid); rm $Dir/pid; fi" | Out-Null
    Start-Sleep -Seconds 1
}

Say "Checking the puck"
$state = ((AdbRun get-state) -join " ").Trim()
if ($state -match "unauthorized")   { Die "the puck is connected but not authorized for adb" }
if ($state -match "more than one")  { Die "more than one adb device is connected; unplug the others" }
if ($state -ne "device")            { Die "no adb device found. Is the puck plugged in, and does 'adb devices' list it? (Windows may need the USB driver, see INSTALL.md)" }
Write-Host ("  model: {0}  Android {1}" -f (ShOut "getprop ro.product.model"), (ShOut "getprop ro.build.version.release"))
if (-not (ShOut "id").StartsWith("uid=0")) { Warn "adb shell is not root here; installing to /system will fail (use -NoAutostart)" }

if ($Uninstall) {
    Say "Stopping puck-tak"
    StopServer
    if (Exists "$Himi.real") {
        Say "Restoring the stock himiwebserver"
        AdbRun remount | Out-Null
        AdbRun shell "rm $Himi; mv $Himi.real $Himi" | Out-Null
        if (Exists "$Himi.real") { Warn "could not restore $Himi (is /system writable?)" } else { Write-Host "  restored" }
    }
    if (Exists $Prop) {
        if ((ShOut "cat $Prop") -eq $PropLine) { AdbRun shell "rm $Prop" | Out-Null; Write-Host "  removed $Prop" }
        else { Warn "$Prop has other settings too; remove the line '$PropLine' from it yourself" }
    }
    AdbRun shell "rm $Wrap" | Out-Null
    if ($Purge) { AdbRun shell "rm -r $Dir" | Out-Null; Write-Host "  deleted $Dir (packages, log, cert)" }
    else { Write-Host "  kept $Dir (packages, log, cert). Use -Uninstall -Purge to delete it." }
    Say "Uninstalled. Reboot the puck to be sure the stock web server is back."
    exit 0
}

if (-not (Test-Path -LiteralPath $Bin)) { Die "bin\puck-tak-armv7 is missing. Download the whole repository, not just this script." }

Say "Installing puck-tak"
StopServer
AdbRun shell "mkdir -p $Dir/data" | Out-Null
AdbRun push $Bin "$Dir/puck-tak" | Out-Null
AdbRun shell "chmod 755 $Dir/puck-tak" | Out-Null
Write-Host "  $(ShOut "$Dir/puck-tak -version")"
if ((Test-Path cert.pem) -and (Test-Path key.pem)) {
    AdbRun push cert.pem "$Dir/cert.pem" | Out-Null
    AdbRun push key.pem "$Dir/key.pem" | Out-Null
    Write-Host "  using cert.pem/key.pem from this folder"
} else {
    Write-Host "  the puck will create its own TLS certificate on first start (or keep the one it has)"
}

# boot wrapper, with LF line endings (Windows may have saved it with CRLF) and the chosen bind address
$text = [IO.File]::ReadAllText((Join-Path (Join-Path $PSScriptRoot "scripts") "himi-wrapper.sh"))
$text = $text.Replace("`r", "").Replace("-bind 192.168.100.1", "-bind $Bind")
$tmp = [IO.Path]::GetTempFileName()
[IO.File]::WriteAllText($tmp, $text, (New-Object Text.UTF8Encoding($false)))
AdbRun push $tmp $Wrap | Out-Null
Remove-Item -LiteralPath $tmp
AdbRun shell "chmod 755 $Wrap" | Out-Null

if (-not $NoAutostart) {
    Say "Setting up auto-start at boot"
    if (-not (Exists $Himi) -and -not (Exists "$Himi.real")) {
        Warn "$Himi is not on this firmware, so the auto-start trick won't work here."
        Warn "puck-tak will run now, but you'll need to start it again after a reboot."
    } else {
        AdbRun remount | Out-Null
        if (-not (Exists "$Himi.real")) { AdbRun shell "mv $Himi $Himi.real" | Out-Null }
        AdbRun shell "rm $Himi 2>/dev/null; ln -s $Wrap $Himi" | Out-Null
        $propText = if (Exists $Prop) { ShOut "cat $Prop" } else { "" }
        if (-not (($propText -split "`n") -contains $PropLine)) {
            AdbRun shell "echo $PropLine >> $Prop; chmod 644 $Prop" | Out-Null
        }
        if ((ShOut "ls -l $Himi") -match "himiwebserver-wrapper") {
            Write-Host "  ok: $Himi -> $Wrap, $Prop sets $PropLine"
        } else {
            Warn "could not replace $Himi (is /system read-only?). Auto-start is NOT set up."
        }
    }
}

Say "Starting puck-tak"
$before = @((ShOut "cat $Dir/server.log 2>/dev/null") -split "`n" | Where-Object { $_ -ne "" }).Count
AdbRun shell "sh $Wrap" | Out-Null
Start-Sleep -Seconds 4   # first start creates the TLS certificate
$log = @((ShOut "cat $Dir/server.log") -split "`n" | Where-Object { $_ -ne "" } | Select-Object -Skip $before)
$log | Select-Object -Last 8 | ForEach-Object { Write-Host "  $_" }
if (-not ($log -match " up: ")) { Die "puck-tak did not report 'up'. The log above should say why." }

Write-Host ""
Write-Host "==> Done. puck-tak is running." -ForegroundColor Green
Write-Host "    Web page (packages):  http://${Bind}:8080/"
Write-Host "    TAK server address:   $Bind"
Write-Host "      plain:  port 8087, protocol TCP"
Write-Host "      TLS:    port 8089, protocol SSL  (self-signed cert, see INSTALL.md)"
