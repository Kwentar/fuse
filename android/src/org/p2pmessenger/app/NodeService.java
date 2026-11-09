package org.p2pmessenger.app;

import android.app.*;
import android.content.*;
import android.content.pm.ServiceInfo;
import android.database.Cursor;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.*;
import android.provider.OpenableColumns;
import org.json.*;
import java.io.*;
import java.lang.Process;
import java.util.UUID;
import java.util.*;
import java.util.concurrent.*;

public final class NodeService extends Service {
    static volatile String notice = "Сеть остановлена";
    static volatile boolean running = false, importing = false;
    static volatile NodeService current;
    ImportQueue imports;DownloadsStore downloads;
    private final ScheduledExecutorService exportWorker=Executors.newSingleThreadScheduledExecutor();
    private final ExecutorService lifecycle = Executors.newSingleThreadExecutor();
    private final ExecutorService disk = Executors.newSingleThreadExecutor();
    private volatile Process process;
    private volatile boolean closing;
    private PowerManager.WakeLock wake;
    private WifiManager.MulticastLock multicast;
    private Engine engine;
    static void start(Context c) {c.startForegroundService(new Intent(c, NodeService.class));}
    @Override public void onCreate() {
        super.onCreate(); engine = new Engine(this);
        current=this;imports=new ImportQueue(this,engine);downloads=new DownloadsStore(this,engine);
        NotificationManager manager = getSystemService(NotificationManager.class);
        manager.createNotificationChannel(new NotificationChannel("network", "P2P соединение", NotificationManager.IMPORTANCE_LOW));
        PendingIntent open = PendingIntent.getActivity(this, 0, new Intent(this, MainActivity.class), PendingIntent.FLAG_IMMUTABLE);
        PendingIntent stop = PendingIntent.getService(this, 1, new Intent(this, NodeService.class).setAction("stop"), PendingIntent.FLAG_IMMUTABLE);
        Notification notification = new Notification.Builder(this, "network").setSmallIcon(android.R.drawable.stat_sys_upload_done)
            .setContentTitle("fuse работает").setContentText("Приём сообщений и файлов • нажмите для открытия")
            .setContentIntent(open).setOngoing(true).addAction(new Notification.Action.Builder(null,"Остановить",stop).build()).build();
        if (Build.VERSION.SDK_INT >= 29) startForeground(1, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        else startForeground(1, notification);
        wake = getSystemService(PowerManager.class).newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "p2pm:network");
        wake.acquire(6L*60*60*1000);
        WifiManager wifi = (WifiManager)getApplicationContext().getSystemService(WIFI_SERVICE);
        if(wifi != null) {multicast=wifi.createMulticastLock("p2pm:discovery"); multicast.setReferenceCounted(false); multicast.acquire();}
        disk.execute(()->imports.run());
        exportWorker.scheduleWithFixedDelay(()->{if(running&&!closing)downloads.scan();},3,2,TimeUnit.SECONDS);
        lifecycle.execute(() -> {
            try {
                if (!engine.exists()) throw new IOException("Сначала создайте ID");
                engine.configure();
                if(closing) return;
                Process child=engine.launch("run"); process=child;
                if(closing) {child.destroy(); return;}
                running=true; notice="Подключаем сеть…";
                // Always drain stdout; private chat messages are never copied to system logcat.
                Thread drain=new Thread(() -> {try(BufferedReader r=new BufferedReader(new InputStreamReader(child.getInputStream()))){while(r.readLine()!=null){}}catch(IOException ignored){}},"node-output");
                drain.setDaemon(true);drain.start();
                int code=child.waitFor(); running=false;
                if(!closing) {notice="Сетевое ядро остановилось ("+code+"). Нажмите «Подключиться»."; stopSelf();}
            } catch(Exception e) {notice="Ошибка сети: "+e.getMessage();running=false;stopSelf();}
        });
    }
    @Override public int onStartCommand(Intent intent,int flags,int id) {
        if(intent != null && "stop".equals(intent.getAction())) {notice="Сеть остановлена пользователем";stopSelf();return START_NOT_STICKY;}
        if(intent != null && "import".equals(intent.getAction())) {
            String raw=intent.getStringExtra("uri"), peer=intent.getStringExtra("peer");
            ArrayList<String> uris=intent.getStringArrayListExtra("uris");if(uris==null)uris=new ArrayList<>();if(raw!=null)uris.add(raw);
            if(peer!=null&&!uris.isEmpty())try{imports.enqueue(peer,uris);notice="Добавлено в подготовку: "+uris.size();}catch(Exception e){notice="Очередь: "+e.getMessage();}
        }
        if(intent!=null&&"retry-import".equals(intent.getAction()))try{imports.retry(intent.getStringExtra("id"));}catch(Exception e){notice=e.getMessage();}
        return START_NOT_STICKY;
    }
    // Android 15 limits dataSync background service time. Save/pause by stopping the node.
    @Override public void onTimeout(int startId,int fgsType) {notice="Android остановил фоновую сеть по времени. Откройте приложение и подключитесь снова.";stopSelf();}
    @Override public void onDestroy() {
        closing=true;running=false;
        if(current==this)current=null;
        if(imports!=null)imports.stop();if(downloads!=null)downloads.stopped=true;exportWorker.shutdownNow();
        Process child=process;if(child!=null)child.destroy();
        disk.shutdownNow();lifecycle.shutdown();
        if(multicast!=null && multicast.isHeld())multicast.release();
        if(wake!=null && wake.isHeld())wake.release();
        stopForeground(STOP_FOREGROUND_REMOVE);super.onDestroy();
    }
    @Override public IBinder onBind(Intent intent){return null;}
}
