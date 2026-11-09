package org.p2pmessenger.app;

import android.content.*;
import android.content.res.AssetFileDescriptor;
import android.media.*;
import android.net.Uri;
import android.os.*;
import android.provider.MediaStore;
import java.io.*;

final class MediaFiles {
    static Intent picker(){
        if(Build.VERSION.SDK_INT>=33){
            Intent i=new Intent(MediaStore.ACTION_PICK_IMAGES).putExtra(MediaStore.EXTRA_PICK_IMAGES_MAX,MediaStore.getPickImagesMaxLimit());
            // We transport bytes, not decode them. Advertise modern formats so HDR/HEVC
            // is not converted for an assumed playback limitation of this application.
            ApplicationMediaCapabilities caps=new ApplicationMediaCapabilities.Builder()
                .addSupportedVideoMimeType("video/hevc").addSupportedVideoMimeType("video/avc")
                .addSupportedHdrType(MediaFeature.HdrType.HDR10).addSupportedHdrType(MediaFeature.HdrType.HDR10_PLUS)
                .addSupportedHdrType(MediaFeature.HdrType.HLG).addSupportedHdrType(MediaFeature.HdrType.DOLBY_VISION).build();
            i.putExtra(MediaStore.EXTRA_MEDIA_CAPABILITIES,caps);
            return i;
        }
        return new Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*")
            .putExtra(Intent.EXTRA_MIME_TYPES,new String[]{"image/*","video/*"}).putExtra(Intent.EXTRA_ALLOW_MULTIPLE,true);
    }
    static InputStream original(Context c,Uri uri)throws IOException{
        if(Build.VERSION.SDK_INT>=31&&"content".equals(uri.getScheme())){
            Bundle options=new Bundle();options.putBoolean(MediaStore.EXTRA_ACCEPT_ORIGINAL_MEDIA_FORMAT,true);
            AssetFileDescriptor afd=c.getContentResolver().openTypedAssetFileDescriptor(uri,"*/*",options);
            if(afd==null)throw new IOException("Не удалось прочитать оригинал");
            return afd.createInputStream();
        }
        InputStream in=c.getContentResolver().openInputStream(uri);if(in==null)throw new IOException("Не удалось открыть файл");return in;
    }
    static String safeName(String s){s=s.replaceAll("[\\\\/\\x00\\r\\n]","_");if(s.isEmpty()||s.equals(".")||s.equals(".."))s="file.bin";return s;}
}
