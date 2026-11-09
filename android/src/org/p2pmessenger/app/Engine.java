package org.p2pmessenger.app;

import android.content.Context;
import org.json.*;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.*;
import java.util.concurrent.TimeUnit;

final class Engine {
    final Context context;
    final File profile;
    final String executable;
    Engine(Context context) {
        this.context=context.getApplicationContext();
        profile = new File(context.getFilesDir(), "profile");
        executable = new File(context.getApplicationInfo().nativeLibraryDir, "libmessenger.so").getAbsolutePath();
    }
    Process launch(String... args) throws IOException {
        ArrayList<String> command = new ArrayList<>(Arrays.asList(executable, "--data-dir", profile.getAbsolutePath(), "--json"));
        Collections.addAll(command, args);
        ProcessBuilder pb = new ProcessBuilder(command).redirectErrorStream(true);
        pb.environment().put("HOME", profile.getParent());
        pb.environment().put("TMPDIR", profile.getParent());
        return pb.start();
    }
    List<JSONObject> call(String... args) throws Exception { return callInput(null, args); }
    List<JSONObject> callInput(String input, String... args) throws Exception {
        Process p = launch(args);
        // Watchdog also closes pipes if the native process stops responding.
        Thread timeout = new Thread(() -> {try {if (!p.waitFor(40, TimeUnit.SECONDS)) p.destroyForcibly();} catch (InterruptedException ignored) {}}, "command-timeout");
        timeout.setDaemon(true); timeout.start();
        try {
            try (OutputStream out = p.getOutputStream()) {if (input != null) out.write(input.getBytes(StandardCharsets.UTF_8));}
            ArrayList<JSONObject> result = new ArrayList<>();
            StringBuilder errors = new StringBuilder();
            try (BufferedReader reader = new BufferedReader(new InputStreamReader(p.getInputStream(), StandardCharsets.UTF_8))) {
                String line;
                while ((line = reader.readLine()) != null) {
                    try {result.add(new JSONObject(line));} catch (JSONException e) {if(errors.length() < 4000) errors.append(line).append('\n');}
                }
            }
            if (p.waitFor() != 0) throw new IOException(result.isEmpty() ? errors.toString() : result.get(result.size()-1).optString("error", result.toString()));
            return result;
        } finally {p.destroy(); timeout.interrupt();}
    }
    JSONObject first(String... args) throws Exception {
        List<JSONObject> rows = call(args);
        if (rows.isEmpty()) throw new IOException("Пустой ответ ядра");
        return rows.get(0);
    }
    boolean exists() {return new File(profile, "identity.key").isFile();}
    void configure() throws Exception {
        File config = new File(profile, "config.json");
        JSONObject value = new JSONObject(new String(Files.readAllBytes(config.toPath()), StandardCharsets.UTF_8));
        value.put("smart_routing", true);
        boolean migrate=context.getSharedPreferences("network_defaults",0).getInt("revision",0)<1;
        if(migrate){
            ByteArrayOutputStream bytes=new ByteArrayOutputStream();
            try(InputStream in=context.getAssets().open("default-network.json")){byte[] block=new byte[4096];int n;while((n=in.read(block))!=-1)bytes.write(block,0,n);}
            JSONObject bundled=new JSONObject(new String(bytes.toByteArray(),StandardCharsets.UTF_8));
            for(String key:new String[]{"bootstrap_nodes","relay_nodes"}){
                LinkedHashSet<String> addresses=new LinkedHashSet<>();
                JSONArray defaults=bundled.getJSONArray(key),existing=value.optJSONArray(key);
                for(int i=0;i<defaults.length();i++)addresses.add(defaults.getString(i));
                if(existing!=null)for(int i=0;i<existing.length();i++)addresses.add(existing.getString(i));
                value.put(key,new JSONArray(addresses));
            }
        }
        File staged=new File(profile,"config.android-next.json");
        try {
            try (FileOutputStream out = new FileOutputStream(staged)) {out.write(value.toString().getBytes(StandardCharsets.UTF_8)); out.getFD().sync();}
            android.system.Os.chmod(staged.getAbsolutePath(),0600);
            Files.move(staged.toPath(),config.toPath(),java.nio.file.StandardCopyOption.ATOMIC_MOVE,java.nio.file.StandardCopyOption.REPLACE_EXISTING);
        }finally{if(staged.exists())staged.delete();}
        if(migrate&&!context.getSharedPreferences("network_defaults",0).edit().putInt("revision",1).commit())throw new IOException("Не удалось сохранить обновление серверов");
    }
}
