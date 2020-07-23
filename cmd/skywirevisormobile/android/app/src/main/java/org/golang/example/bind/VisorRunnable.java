package org.golang.example.bind;

import skywiremob.Skywiremob;

public class VisorRunnable implements Runnable {
    @Override
    public void run() {
        android.os.Process.setThreadPriority(android.os.Process.THREAD_PRIORITY_BACKGROUND);

        Skywiremob.runVisor();
        /*
         * Code you want to run on the thread goes here
         */
    }
}