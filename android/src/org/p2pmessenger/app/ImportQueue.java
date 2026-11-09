package org.p2pmessenger.app;

import android.content.*;
import android.database.Cursor;
import android.net.Uri;
import android.provider.OpenableColumns;
import org.json.*;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.*;

final class ImportQueue {
    private final Context context;private final Engine engine;private final SharedPreferences prefs;
    private final Object lock=new Object();private volatile boolean stopped;
    private volatile String active="";private volatile long copied,size;
    ImportQueue(Context c,Engine e){context=c;engine=e;prefs=c.getSharedPreferences("import_queue",0);}
    private JSONArray read(){try{return new JSONArray(prefs.getString("queue","[]"));}catch(Exception e){throw new IllegalStateException("Очередь повреждена",e);}}
    private void save(JSONArray rows)throws IOException{if(!prefs.edit().putString("queue",rows.toString()).commit())throw new IOException("Не удалось сохранить очередь");}
    void enqueue(String peer,List<String> uris)throws Exception{synchronized(lock){JSONArray rows=read();for(String raw:new LinkedHashSet<>(uris)){JSONObject r=new JSONObject();r.put("id",UUID.randomUUID().toString());r.put("uri",raw);r.put("peer",peer);r.put("name","Ожидает подготовки");r.put("state","queued");rows.put(r);}save(rows);lock.notifyAll();}}
    JSONArray snapshot(){synchronized(lock){JSONArray rows=read();for(int i=0;i<rows.length();i++){JSONObject r=rows.optJSONObject(i);if(r!=null&&active.equals(r.optString("id"))){try{r.put("copied",copied);r.put("size",size);}catch(JSONException ignored){}}}return rows;}}
    void retry(String id)throws Exception{synchronized(lock){JSONArray rows=read();for(int i=0;i<rows.length();i++){JSONObject r=rows.getJSONObject(i);if(id.equals(r.optString("id")))r.put("state","queued").remove("error");}save(rows);lock.notifyAll();}}
    void stop(){stopped=true;synchronized(lock){lock.notifyAll();}}
    private void update(JSONObject value,boolean remove)throws Exception{synchronized(lock){JSONArray old=read(),next=new JSONArray();for(int i=0;i<old.length();i++){JSONObject row=old.getJSONObject(i);if(row.getString("id").equals(value.getString("id"))){if(!remove)next.put(value);}else next.put(row);}save(next);}}
    void run(){
        while(!stopped){JSONObject item=null;try{
            synchronized(lock){JSONArray rows=read();for(int i=0;i<rows.length();i++){JSONObject r=rows.getJSONObject(i);if(!"failed".equals(r.optString("state"))){item=r;break;}}if(item==null){lock.wait(1000);continue;}}
            active=item.getString("id");copied=0;size=0;copy(item);update(item,true);
        }catch(InterruptedException e){return;}catch(Exception e){if(stopped)return;if(item!=null)try{item.put("state","failed").put("error",e.getMessage());update(item,false);}catch(Exception ignored){}NodeService.notice="Подготовка: "+e.getMessage();}finally{active="";}}
    }
    private void copy(JSONObject item)throws Exception{
        Uri uri=Uri.parse(item.getString("uri"));String name=item.optBoolean("staged")?item.getString("name"):"file.bin";
        if(item.optBoolean("staged"))size=item.optLong("size");
        else try(Cursor c=context.getContentResolver().query(uri,new String[]{OpenableColumns.DISPLAY_NAME,OpenableColumns.SIZE},null,null,null)){if(c!=null&&c.moveToFirst()){int n=c.getColumnIndex(OpenableColumns.DISPLAY_NAME),s=c.getColumnIndex(OpenableColumns.SIZE);if(n>=0&&!c.isNull(n))name=c.getString(n);if(s>=0&&!c.isNull(s))size=c.getLong(s);}}
        name=MediaFiles.safeName(name);if(name.getBytes(StandardCharsets.UTF_8).length>255)throw new IOException("Слишком длинное имя: "+name);
        item.put("name",name).put("state","copying").put("size",size);update(item,false);
        File dir=new File(context.getFilesDir(),"outbox/"+item.getString("id"));if(!dir.exists()&&!dir.mkdirs())throw new IOException("Не удалось создать папку исходника");
        File file=new File(dir,name);
        // A successfully staged copy survives an uncertain queue-file response.
        if(!item.optBoolean("staged")){
            try(InputStream in=MediaFiles.original(context,uri);FileOutputStream out=new FileOutputStream(file)){
                byte[] block=new byte[128*1024];int n;while((n=in.read(block))!=-1){if(stopped)throw new IOException("Подготовка приостановлена");out.write(block,0,n);copied+=n;}out.getFD().sync();
            }
            item.put("staged",true);update(item,false);
        }
        boolean ready=false;for(int i=0;i<60&&!stopped;i++){try{engine.call("status");ready=true;break;}catch(Exception e){Thread.sleep(500);}}
        if(!ready)throw new IOException("Сеть не запущена — нажмите продолжить позже");
        engine.call("queue-file",item.getString("peer"),file.getAbsolutePath(),item.getString("id"));
        NodeService.notice="Добавлено в очередь: "+name;
    }
}
