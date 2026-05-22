Add-Type -AssemblyName System.Drawing

$logoPath = "D:\WorkSpace\AI\sypora\assets\logo_original.png"
try {
    $img = [System.Drawing.Image]::FromFile($logoPath)
    Write-Host "Logo: $($img.Width)x$($img.Height) px"
    Write-Host "PixelFormat: $($img.PixelFormat)"
    $img.Dispose()
} catch {
    Write-Host "Error loading logo: $($_.Exception.Message)"
}

$icoPath = "D:\WorkSpace\AI\sypora\assets\icon.ico"
try {
    $ico = [System.Drawing.Icon]::ExtractAssociatedIcon($icoPath)
    Write-Host "Current ICO: $($ico.Width)x$($ico.Height)"
    $ico.Dispose()
} catch {
    Write-Host "Error loading ico: $($_.Exception.Message)"
}
