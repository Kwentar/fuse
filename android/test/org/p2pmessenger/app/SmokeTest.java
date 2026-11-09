package org.p2pmessenger.app;

import android.app.*;
import android.content.*;
import android.os.Bundle;
import org.json.*;
import java.io.*;
import java.security.MessageDigest;
import java.util.*;

// Included only in the isolated emulator APK, never in the ARM64 APK.
public class SmokeTest extends Instrumentation {
    private Bundle args;
    @Override public void onCreate(Bundle arguments){args=arguments;start();}
    void report(String key,String value){Bundle b=new Bundle();b.putString(key,value);sendStatus(0,b);}
    static String hash(File path) throws Exception {MessageDigest d=MessageDigest.getInstance("SHA-256");try(InputStream in=new FileInputStream(path)){byte[] b=new byte[131072];int n;while((n=in.read(b))!=-1)d.update(b,0,n);}StringBuilder s=new StringBuilder();for(byte b:d.digest())s.append(String.format(Locale.ROOT,"%02x",b&255));return s.toString();}
    static String hashDocument(Context c,String uri)throws Exception{MessageDigest d=MessageDigest.getInstance("SHA-256");try(InputStream in=c.getContentResolver().openInputStream(android.net.Uri.parse(uri))){if(in==null)throw new IOException("Missing exported document");byte[] b=new byte[131072];int n;while((n=in.read(b))!=-1)d.update(b,0,n);}StringBuilder s=new StringBuilder();for(byte b:d.digest())s.append(String.format(Locale.ROOT,"%02x",b&255));return s.toString();}
    void migration(Context context,Engine engine)throws Exception{
        File cfg=new File(engine.profile,"config.json"),key=new File(engine.profile,"identity.key");String keyHash=hash(key);
        JSONObject original=new JSONObject(new String(java.nio.file.Files.readAllBytes(cfg.toPath()),java.nio.charset.StandardCharsets.UTF_8));
        String old=original.getJSONArray("relay_nodes").getString(original.getJSONArray("relay_nodes").length()-1);
        original.put("relay_nodes",new JSONArray().put(old));original.put("bootstrap_nodes",new JSONArray().put(old));String timeout=original.getString("send_timeout");
        try(FileOutputStream out=new FileOutputStream(cfg)){out.write(original.toString().getBytes(java.nio.charset.StandardCharsets.UTF_8));out.getFD().sync();}
        context.getSharedPreferences("network_defaults",0).edit().clear().commit();
        engine.configure();engine.configure();
        JSONObject result=new JSONObject(new String(java.nio.file.Files.readAllBytes(cfg.toPath()),java.nio.charset.StandardCharsets.UTF_8));
        for(String field:new String[]{"relay_nodes","bootstrap_nodes"}){JSONArray a=result.getJSONArray(field);Set<String> seen=new HashSet<>();for(int i=0;i<a.length();i++)seen.add(a.getString(i));if(a.length()<2||a.length()!=seen.size()||!seen.contains(old)||a.getString(0).equals(old))throw new IOException("Server migration failed");}
        if(!hash(key).equals(keyHash)||!timeout.equals(result.getString("send_timeout")))throw new IOException("Profile changed unexpectedly");
        report("migration","PASS: new relay first; old retained; identity unchanged; repeat safe");
    }
    @Override public void onStart(){
        Bundle result=new Bundle();
        try {
            Context context=getTargetContext();Engine engine=new Engine(context);
            engine.call("init");String id=engine.first("id").getString("peer_id");report("peer_id",id);
            migration(context,engine);
            Intent launch=new Intent(context,MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK|Intent.FLAG_ACTIVITY_CLEAR_TASK);
            startActivitySync(launch);waitForIdleSync();
            long until=System.currentTimeMillis()+90000;boolean online=false;
            while(System.currentTimeMillis()<until){try{if("online".equals(engine.first("status").optString("state"))){online=true;break;}}catch(Exception ignored){}Thread.sleep(1000);}
            if(!online)throw new IOException("Android node did not come online: "+NodeService.notice);
            report("network","online");
            String peer=args==null?null:args.getString("peer");
            if(peer!=null&&!peer.isEmpty()){
                engine.call("add-contact",peer,"Windows test");
                // Controller adds this Android ID to the Windows contact list.
                Thread.sleep(15000);
                JSONObject sent=engine.callInput("Привет с Android 👋\nВторая строка","send",peer,"--stdin").get(0);
                if(!"delivered".equals(sent.optString("status")))throw new IOException("Text: "+sent);
                report("text",sent.toString());
                File source=new File(context.getFilesDir(),"from-android.bin");
                try(FileOutputStream out=new FileOutputStream(source)){byte[] block=new byte[1024*1024];for(int i=0;i<block.length;i++)block[i]=(byte)(i%251);for(int i=0;i<20;i++)out.write(block);out.getFD().sync();}
                context.startForegroundService(new Intent(context,NodeService.class).setAction("import").putExtra("uri","content://org.p2pmessenger.app.test/source").putExtra("peer",peer));
                String fileID="";until=System.currentTimeMillis()+30000;
                while(fileID.isEmpty()&&System.currentTimeMillis()<until){for(JSONObject row:engine.call("files",peer)){if("out".equals(row.optString("direction"))&&"from-android.bin".equals(row.optString("name")))fileID=row.getString("file_id");}Thread.sleep(100);}
                if(fileID.isEmpty())throw new IOException("SAF-style import failed: "+NodeService.notice);
                engine.call("pause-file",fileID);
                boolean paused=false;until=System.currentTimeMillis()+15000;
                while(!paused&&System.currentTimeMillis()<until){for(JSONObject row:engine.call("files",peer)){if(fileID.equals(row.optString("file_id"))&&"paused".equals(row.optString("status")))paused=true;}Thread.sleep(100);}
                if(!paused)throw new IOException("Pause did not persist");
                engine.call("resume-file",fileID);report("resume","paused task resumed");
                until=System.currentTimeMillis()+240000;boolean delivered=false,received=false;
                while(System.currentTimeMillis()<until){
                    for(JSONObject row:engine.call("files",peer)){
                        if(fileID.equals(row.optString("file_id"))&&"delivered".equals(row.optString("status"))){delivered=true;report("sent_file",row.optString("route")+":"+row.optLong("progress"));}
                        if("in".equals(row.optString("direction"))&&"stored".equals(row.optString("status"))&&"from-windows.bin".equals(row.optString("name"))&&row.optString("export_path").startsWith("content://")){if(!hashDocument(context,row.getString("export_path")).equals(row.optString("sha256")))throw new IOException("Downloaded SHA mismatch");received=true;}
                    }
                    if(delivered&&received)break;Thread.sleep(2000);
                }
                if(!delivered||!received)throw new IOException("File timeout: sent="+delivered+" received="+received);
                report("files","20 MiB each direction, inbound SHA verified");
            }
            result.putString("result","PASS");result.putString("peer_id",id);finish(Activity.RESULT_OK,result);
        }catch(Throwable e){result.putString("result","FAIL");result.putString("error",e.toString());finish(Activity.RESULT_CANCELED,result);}
    }
}
