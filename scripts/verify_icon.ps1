Add-Type -AssemblyName System.Drawing

$icoPath = "D:\WorkSpace\AI\sypora\assets\icon.ico"
try {
    $ico = [System.Drawing.Icon]::ExtractAssociatedIcon($icoPath)
    Write-Host "Icon: $($ico.Width)x$($ico.Height)"
    $ico.Dispose()
} catch {
    Write-Host "Error: $($_.Exception.Message)"
}
$fi = Get-Item $icoPath
Write-Host "File size: $($fi.Length) bytes"
