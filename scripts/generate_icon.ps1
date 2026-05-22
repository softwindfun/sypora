Add-Type -AssemblyName System.Drawing

$src = "D:\WorkSpace\AI\sypora\assets\logo_original.png"
$dst = "D:\WorkSpace\AI\sypora\assets\icon.ico"
$sizes = @(16, 32, 48, 256)

$original = [System.Drawing.Image]::FromFile($src)
$imageStreams = @()

foreach ($sz in $sizes) {
    $bmp = New-Object System.Drawing.Bitmap($original, $sz, $sz)
    $ms = New-Object System.IO.MemoryStream
    $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
    $imageStreams += $ms
    $bmp.Dispose()
}
$original.Dispose()

$fs = [System.IO.File]::OpenWrite($dst)
$bw = New-Object System.IO.BinaryWriter($fs)

# ICONDIR header
$bw.Write([uint16]0)  # reserved
$bw.Write([uint16]1)  # type = ICO
$bw.Write([uint16]$imageStreams.Count)

# ICONDIRENTRY
$offset = 6 + 16 * $imageStreams.Count
for ($i = 0; $i -lt $imageStreams.Count; $i++) {
    $sz = $sizes[$i]
    $dataSize = [int]$imageStreams[$i].Length
    $bw.Write([byte]$(if ($sz -ge 256) { 0 } else { $sz }))
    $bw.Write([byte]$(if ($sz -ge 256) { 0 } else { $sz }))
    $bw.Write([byte]0)   # color_count
    $bw.Write([byte]0)   # reserved
    $bw.Write([uint16]1)  # planes
    $bw.Write([uint16]32) # bit_count
    $bw.Write([uint32]$dataSize)
    $bw.Write([uint32]$offset)
    $offset += $dataSize
}

# Image data
foreach ($ms in $imageStreams) {
    $data = $ms.ToArray()
    $bw.Write($data)
    $ms.Dispose()
}

$bw.Close()
$fs.Close()

Write-Host "Generated $dst with $($sizes.Count) sizes: $($sizes -join ', ')"
