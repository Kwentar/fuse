import Foundation
import Darwin

@main
struct BridgeTests {
    static func require(_ condition: @autoclosure () -> Bool, _ message: String) throws {
        if !condition() { throw CoreFailure(message: "TEST FAILED: " + message) }
    }
    static func main() async throws {
        let root=URL(fileURLWithPath:CommandLine.arguments[1])
        let literal="Привет 🐈\nВторая строка; $(not-a-command) `literal` \\\""
        let input=String(repeating:literal,count:1500)
        let result=try await CoreBridge.execute(executable:URL(fileURLWithPath:"/usr/bin/python3"),arguments:[root.appendingPathComponent("macos/Tests/pipe_fixture.py").path,"--name",literal],input:input)
        try require(result.exitCode==4,"nonzero delivery result discarded")
        try require(result.rows.count==2,"JSONL rows")
        try require(result.diagnostic=="IPC_RESULT_UNKNOWN","stderr tail diagnostic lost")
        let echoed=try JSONSerialization.jsonObject(with:result.rows[0]) as! [String:Any]
        try require(echoed["text"] as? String==input,"Unicode/newline stdin did not roundtrip")
        try require((echoed["arguments"] as? [String])?.last==literal,"literal argument changed")
        let delivery=try JSONDecoder().decode(Delivery.self,from:result.rows[1])
        try require(delivery.status=="delivery_unknown","lost ACK state")
        print("PASS: concurrent stdout/stderr/stdin, JSONL, literal arguments, nonzero delivery result")

        let json=#"{"file_id":"550e8400-e29b-41d4-a716-446655440000","peer_id":"peer","name":"big","size":9007199254740993,"progress":9007199254740992,"sha256":"","direction":"out","status":"paused","path":"/tmp/file","time":"2026-11-04T00:00:00Z"}"#
        let transfer=try JSONDecoder().decode(Transfer.self,from:Data(json.utf8))
        try require(transfer.size-transfer.progress==1,"Int64 precision above 2^53")
        try require("🐈".utf8.count==4 && "я".utf8.count==2,"UTF-8 validation")
        try require(Route(stage:"trying_direct",route:"direct").connected==false,"attempt displayed as connected")
        try require(Route(stage:"disconnected",route:"lan").label=="Нет соединения","stale route displayed")
        print("PASS: exact Int64 file counters, UTF-8 lengths, truthful route stages")

        let dir=URL(fileURLWithPath:NSTemporaryDirectory()).appendingPathComponent("p2pm-swift-"+UUID().uuidString)
        try FileManager.default.createDirectory(at:dir,withIntermediateDirectories:true,attributes:[.posixPermissions:0o700])
        defer { try? FileManager.default.removeItem(at:dir) }
        let source=dir.appendingPathComponent("source"),dest=dir.appendingPathComponent("destination")
        let content=Data(repeating:0x73,count:3*1024*1024+1)
        try content.write(to:source);try Data("existing".utf8).write(to:dest)
        try FileExport.copy(source:source,destination:dest,expectedSize:Int64(content.count))
        let saved=try Data(contentsOf:dest)
        try require(saved==content,"streamed save bytes")
        let link=dir.appendingPathComponent("link")
        try FileManager.default.createSymbolicLink(at:link,withDestinationURL:source)
        var rejected=false
        do {try FileExport.copy(source:link,destination:dest,expectedSize:Int64(content.count))} catch {rejected=true}
        try require(rejected,"symlink source accepted")
        let after=try Data(contentsOf:dest)
        try require(after==content,"failed save damaged existing destination")
        print("PASS: atomic streaming export, overwrite, symlink rejection, original destination preserved on failure")
        let album=dir.appendingPathComponent("Альбом")
        try FileManager.default.createDirectory(at:album,withIntermediateDirectories:true)
        for i in 0..<1010 {
            let name=i<1000 ? "фото-\(i).jpg" : "видео-\(i).mp4"
            try Data([UInt8(i%251)]).write(to:album.appendingPathComponent(name))
        }
        try FileManager.default.createSymbolicLink(at:album.appendingPathComponent("skip-link"),withDestinationURL:source)
        let scan=FileEnumeration.scan([album,album.appendingPathComponent("фото-0.jpg")])
        try require(scan.files.count==1010,"recursive multi-file enumeration/deduplication")
        try require(scan.errors.count==1,"symlink not skipped")
        let stagingDir=dir.appendingPathComponent("queue")
        try FileManager.default.createDirectory(at:stagingDir,withIntermediateDirectories:true,attributes:[.posixPermissions:0o700])
        let bridge=await CoreBridge(profile:stagingDir,executable:URL(fileURLWithPath:"/usr/bin/false"))
        let queue=await FileQueue(core:bridge)
        await queue.stage([album],peer:"test-peer")
        let staged=await queue.staged
        try require(staged.count==1010 && Set(staged.map(\.id)).count==1010,"durable unique file IDs")
        await queue.setPaused(true)
        let persisted=try JSONDecoder().decode([StagedFile].self,from:Data(contentsOf:stagingDir.appendingPathComponent("macos-staging.json")))
        try require(persisted.count==1010 && persisted.allSatisfy(\.paused),"batch pause not persisted")
        try require(Set(persisted.map(\.id))==Set(staged.map(\.id)),"IDs changed on pause")
        print("PASS: 1010 mixed file paths, folder flattening, symlink skip, durable UUID staging and batch pause")
    }
}
