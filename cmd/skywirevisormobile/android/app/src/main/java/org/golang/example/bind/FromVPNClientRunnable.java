package org.golang.example.bind;

import java.io.File;
import java.io.FileOutputStream;

import skywiremob.Skywiremob;

public class FromVPNClientRunnable implements Runnable {
    private FileOutputStream out;

    public FromVPNClientRunnable(FileOutputStream out) {
        this.out = out;
    }

    @Override
    public void run() {
        android.os.Process.setThreadPriority(android.os.Process.THREAD_PRIORITY_BACKGROUND);

        byte[] readData = new byte[]{};

            while (true) {
                try {
                    // Read the incoming packet from the tunnel.
                    readData = Skywiremob.read();
                    int length = readData.length;
                    if (length > 0) {
                        out.write(readData, 0, length);
                        //Skywiremob.printString("WROTE PACKET TO TUN");
                    }
                } catch (Exception e) {
                    String bytes = "[";
                    for (int i = 0; i < readData.length; i++) {
                        bytes += readData[i] + ", ";
                    }
                    bytes += "]";

                    String stackTrace = "";
                    StackTraceElement[] stackTraceArr = e.getStackTrace();
                    for (int i = 0; i < stackTraceArr.length; i++) {
                        stackTrace += stackTraceArr[i].toString() + "\n";
                    }
                    //Skywiremob.printString("EXCEPTION IN FromVPNClientRunnable WHILRE WRITING " + bytes + ": " + stackTrace);
                }
            }
    }
}
