package org.golang.example.bind;

import java.io.IOException;
import java.io.OutputStream;
import java.io.PrintWriter;
import java.net.Socket;

import skywiremob.Skywiremob;

public class VisorRunnable implements Runnable {
    @Override
    public void run() {
        android.os.Process.setThreadPriority(android.os.Process.THREAD_PRIORITY_BACKGROUND);

        /*Skywiremob.startListening();
        Socket s = null;
        try {
            s = new Socket("127.0.0.1", 7890);

            OutputStream out = s.getOutputStream();

            PrintWriter output = new PrintWriter(out);

            output.println("Hello there!");
            output.flush();
        } catch (IOException e) {
            Skywiremob.printString("SOCKET WRITING ERROR: " + e.getMessage());
        }*/



        Skywiremob.prepareLogger();
        Skywiremob.prepareVisor();

        Skywiremob.printString("PREPARED VISOR");

        Skywiremob.prepareVPNClient();
        Skywiremob.printString("PREPARED VPN CLIENT");
        Skywiremob.shakeHands();
        Skywiremob.printString("SHOOK HANDS");

        Skywiremob.printDmsgServers();

        Skywiremob.startListening();



        Skywiremob.waitForVisorToStop();
        //kywiremob
        //var visor = Skywiremob./p
        //Skywiremob.runVisor();


        /*
         * Code you want to run on the thread goes here
         */
    }
}