package org.p2pmessenger.app;

import android.content.*;
import android.database.*;
import android.net.Uri;
import android.os.ParcelFileDescriptor;
import android.provider.OpenableColumns;
import java.io.*;

// Read-only test document; absent from the distributable APK.
public class TestFileProvider extends ContentProvider {
    public boolean onCreate(){return true;}
    public String getType(Uri uri){return "application/octet-stream";}
    public Cursor query(Uri uri,String[] projection,String selection,String[] args,String sort){MatrixCursor c=new MatrixCursor(new String[]{OpenableColumns.DISPLAY_NAME});c.addRow(new Object[]{"from-android.bin"});return c;}
    public ParcelFileDescriptor openFile(Uri uri,String mode) throws FileNotFoundException {if(!"r".equals(mode))throw new FileNotFoundException("Read only");return ParcelFileDescriptor.open(new File(getContext().getFilesDir(),"from-android.bin"),ParcelFileDescriptor.MODE_READ_ONLY);}
    public Uri insert(Uri uri,ContentValues values){throw new UnsupportedOperationException();}
    public int update(Uri uri,ContentValues values,String selection,String[] args){throw new UnsupportedOperationException();}
    public int delete(Uri uri,String selection,String[] args){throw new UnsupportedOperationException();}
}
