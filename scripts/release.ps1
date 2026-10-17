param(
    [Parameter(Mandatory = $true)]
    [string]$Version
)

$ErrorActionPreference = 'Stop'
if ($Version -notmatch '^v\d+\.\d+(?:\.\d+){0,2}$') {
    throw '版本号必须是 v 开头的数字版本。'
}
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
$names = @('GOOS', 'GOARCH', 'GOARM', 'CGO_ENABLED')
$saved = @{}
foreach ($name in $names) { $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
try {
    $commit = git rev-parse HEAD
    if ($LASTEXITCODE -ne 0) { throw '无法读取提交版本。' }
    $builtAt = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    $prefix = 'github.com/wnddd839/codebuddy-proxy/internal/version'
    $flags = "-s -w -X $prefix.Version=$Version -X $prefix.Commit=$commit -X $prefix.BuiltAt=$builtAt"
    $targets = @(
        @{ OS='windows'; Arch='amd64'; Name='windows-x64.exe' },
        @{ OS='linux'; Arch='amd64'; Name='linux-amd64' },
        @{ OS='linux'; Arch='arm64'; Name='linux-arm64' },
        @{ OS='linux'; Arch='arm'; Arm='7'; Name='linux-armv7' },
        @{ OS='linux'; Arch='arm'; Arm='6'; Name='linux-armv6' },
        @{ OS='darwin'; Arch='arm64'; Name='darwin-arm64' },
        @{ OS='darwin'; Arch='amd64'; Name='darwin-amd64' }
    )
    New-Item -ItemType Directory -Force (Join-Path $root 'releases') | Out-Null
    $checksums = @()
    foreach ($target in $targets) {
        $env:GOOS = $target.OS
        $env:GOARCH = $target.Arch
        $env:GOARM = $target.Arm
        $env:CGO_ENABLED = '0'
        $name = "codebuddy-proxy-$($target.Name)"
        $output = Join-Path $root "releases/$name"
        go build -trimpath -ldflags $flags -o $output ./cmd/codebuddy-proxy
        if ($LASTEXITCODE -ne 0) { throw "构建失败：$name" }
        $hash = (Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash.ToLowerInvariant()
        $checksums += "$hash  $name"
        Write-Output "构建完成：$name"
    }
    [IO.File]::WriteAllText((Join-Path $root 'releases/SHA256SUMS.txt'), ($checksums -join "`n") + "`n")
    Copy-Item -LiteralPath (Join-Path $root '.env.example') -Destination (Join-Path $root 'releases/default.env.example')
} finally {
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process') }
    Pop-Location
}
