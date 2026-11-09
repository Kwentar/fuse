package org.p2pmessenger.app;

import android.content.*;
import android.net.Uri;
import android.os.*;
import android.provider.*;
import android.webkit.MimeTypeMap;
import org.json.*;
import java.io.*;
import java.security.MessageDigest;
import java.util.*;

final class DownloadsStore {
    private final Context context;private final Engine engine;private final android.content.SharedPreferences prefs;
    volatile boolean stopped;static volatile String notice="";
    DownloadsStore(Context c,Engine e){context=c;engine=e;prefs=c.getSharedPreferences("downloads",0);}
    static String folder(Context c){return c.getSharedPreferences("downloads",0).getString("tree","");}
    static void folder(Context c,Uri uri){c.getSharedPreferences("downloads",0).edit().putString("tree",uri.toString()).apply();}
    static String error(Context c,JSONObject r){return c.getSharedPreferences("downloads",0).getString(r.optString("peer_id")+":"+r.optString("file_id")+":error","");}
    void scan(){if(stopped)return;try{for(JSONObject r:engine.call("files")){if(stopped)return;if("in".equals(r.optString("direction"))&&"stored".equals(r.optString("status"))&&r.optString("export_path").isEmpty()){
        String key=r.optString("peer_id")+":"+r.optString("file_id")+":error";
        try{publish(r);prefs.edit().remove(key).apply();}catch(Exception e){notice="Сохранение: "+e.getMessage();prefs.edit().putString(key,notice).apply();}
    }}}catch(Exception e){notice="Сохранение: "+e.getMessage();}}
    private void publish(JSONObject r)throws Exception{
        String key=r.getString("peer_id")+":"+r.getString("file_id"),saved=prefs.getString(key,"");Uri uri=saved.isEmpty()?null:Uri.parse(saved);
        String name=MediaFiles.safeName(r.getString("name"));String ext=MimeTypeMap.getFileExtensionFromUrl(name);String mime=MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext.toLowerCase(Locale.ROOT));if(mime==null)mime="application/octet-stream";
        boolean mediaStore=uri==null?folder(context).isEmpty():prefs.getBoolean(key+":mediaStore","media".equals(uri.getAuthority()));
        if(uri==null){
            if(!mediaStore){Uri tree=Uri.parse(folder(context));uri=DocumentsContract.createDocument(context.getContentResolver(),DocumentsContract.buildDocumentUriUsingTree(tree,DocumentsContract.getTreeDocumentId(tree)),mime,name);}
            else if(Build.VERSION.SDK_INT>=29){ContentValues v=new ContentValues();v.put(MediaStore.MediaColumns.DISPLAY_NAME,name);v.put(MediaStore.MediaColumns.MIME_TYPE,mime);v.put(MediaStore.MediaColumns.RELATIVE_PATH,Environment.DIRECTORY_DOWNLOADS+"/P2PMessenger");v.put(MediaStore.MediaColumns.IS_PENDING,1);uri=context.getContentResolver().insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI,v);}
            else{throw new IOException("Выберите папку загрузок в настройках приложения");}
            if(uri==null)throw new IOException("Не удалось создать документ");
            if(!prefs.edit().putString(key,uri.toString()).putBoolean(key+":mediaStore",mediaStore).commit())throw new IOException("Не удалось сохранить место загрузки");
        }
        if(!prefs.getBoolean(key+":complete",false)){
            notice="Сохраняем "+name;MessageDigest hash=MessageDigest.getInstance("SHA-256");
            try(InputStream in=new FileInputStream(r.getString("path"));OutputStream out=context.getContentResolver().openOutputStream(uri,"wt")){
                if(out==null)throw new IOException("Нет доступа к папке загрузок");byte[] b=new byte[128*1024];int n;while((n=in.read(b))!=-1){if(stopped)throw new IOException("Сохранение продолжится после запуска");hash.update(b,0,n);out.write(b,0,n);}out.flush();
            }
            StringBuilder sha=new StringBuilder();for(byte b:hash.digest())sha.append(String.format(Locale.ROOT,"%02x",b&255));if(!sha.toString().equals(r.getString("sha256")))throw new IOException("Контрольная сумма копии не совпала");
            if(Build.VERSION.SDK_INT>=29&&mediaStore){ContentValues v=new ContentValues();v.put(MediaStore.MediaColumns.IS_PENDING,0);context.getContentResolver().update(uri,v,null,null);}
            if(!prefs.edit().putBoolean(key+":complete",true).commit())throw new IOException("Не удалось сохранить подтверждение загрузки");
        }
        engine.call("publish-file",r.getString("file_id"),uri.toString());notice="Сохранено в загрузки: "+name;
    }
}
