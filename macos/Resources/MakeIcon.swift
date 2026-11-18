import AppKit
let output = CommandLine.arguments[1]
let image = NSImage(size: NSSize(width: 1024, height: 1024))
for pixels in [16,32,64,128,256,512,1024] {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    rep.size = NSSize(width:1024,height:1024)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep:rep)
    let rect=NSRect(x:80,y:80,width:864,height:864)
    let shape=NSBezierPath(roundedRect:rect,xRadius:194,yRadius:194)
    NSGradient(starting:NSColor(srgbRed:0.20,green:0.60,blue:0.51,alpha:1),ending:NSColor(srgbRed:0.07,green:0.32,blue:0.29,alpha:1))!.draw(in:shape,angle:270)
    if let symbol=NSImage(systemSymbolName:"bubble.left.and.bubble.right.fill",accessibilityDescription:nil) {
        let config=NSImage.SymbolConfiguration(pointSize:460,weight:.medium).applying(.init(paletteColors:[.white]))
        symbol.withSymbolConfiguration(config)?.draw(in:NSRect(x:255,y:270,width:514,height:484))
    }
    NSGraphicsContext.restoreGraphicsState()
    image.addRepresentation(rep)
}
let iconset=URL(fileURLWithPath:output).deletingLastPathComponent().appendingPathComponent("AppIcon.iconset")
try FileManager.default.createDirectory(at:iconset,withIntermediateDirectories:true)
for size in [16,32,128,256,512] {
    for scale in [1,2] {
        let pixels=size*scale
        let rep=image.representations.compactMap{$0 as? NSBitmapImageRep}.first{$0.pixelsWide==pixels}!
        try rep.representation(using:.png,properties:[:])!.write(to:iconset.appendingPathComponent("icon_\(size)x\(size)\(scale==2 ? "@2x" : "").png"))
    }
}
let process=Process();process.executableURL=URL(fileURLWithPath:"/usr/bin/iconutil");process.arguments=["-c","icns",iconset.path,"-o",output];try process.run();process.waitUntilExit()
guard process.terminationStatus==0 else { fatalError("iconutil failed") }
try FileManager.default.removeItem(at:iconset)
