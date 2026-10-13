# Installs MangoMan on Windows:  irm __SITE__/install.ps1 | iex
# A file fetched by PowerShell carries no "downloaded from the internet" mark,
# so Windows does not stop it with "Windows protected your PC". It goes to
# %LOCALAPPDATA%\MangoMan, with shortcuts in the Start menu and on the desktop.
# MANGOMAN_SITE fetches from another copy of the site and MANGOMAN_NO_START
# skips starting MangoMan (both for the CI checks). Works in PowerShell 5.1.
& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # the progress bar slows downloads in PowerShell 5.1 many times over
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $site = if ($env:MANGOMAN_SITE) { $env:MANGOMAN_SITE } else { '__SITE__' }
    $file = 'mangoman-windows-amd64.exe'
    $dir = Join-Path $env:LOCALAPPDATA 'MangoMan'
    $exe = Join-Path $dir 'mangoman.exe'
    $new = "$exe.new"
    New-Item -ItemType Directory -Force -Path $dir | Out-Null

    Write-Host 'Downloading MangoMan for Windows (about 10 MB)...'
    Invoke-WebRequest -UseBasicParsing "$site/$file" -OutFile $new
    $sums = [string](Invoke-WebRequest -UseBasicParsing "$site/SHA256SUMS.txt").Content
    $want = ''
    foreach ($line in $sums -split "`n") {
        $f = $line.Trim() -split '\s+'
        if ($f.Count -eq 2 -and $f[1] -eq $file) { $want = $f[0] }
    }
    $got = (Get-FileHash $new -Algorithm SHA256).Hash.ToLower()
    if (-not $want -or $want -ne $got) {
        Remove-Item $new
        throw 'The download was damaged. Please run the line again.'
    }
    try {
        Move-Item -Force $new $exe
    } catch {
        Remove-Item $new
        throw 'MangoMan is running. Close its black window, then run the line again.'
    }

    $shell = New-Object -ComObject WScript.Shell
    foreach ($place in [Environment]::GetFolderPath('Programs'), [Environment]::GetFolderPath('Desktop')) {
        $link = $shell.CreateShortcut((Join-Path $place 'MangoMan.lnk'))
        $link.TargetPath = $exe
        $link.WorkingDirectory = $dir
        $link.Save()
    }
    Write-Host 'MangoMan is installed. Next time, open MangoMan from the Start menu or your desktop.'
    if (-not $env:MANGOMAN_NO_START) { Start-Process $exe }
}
