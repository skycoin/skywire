package org.golang.example.bind;

import skywiremob.Skywiremob;

public class VisorRunnable implements Runnable {
    @Override
    public void run() {
        android.os.Process.setThreadPriority(android.os.Process.THREAD_PRIORITY_BACKGROUND);

        Skywiremob.prepareLogger();
        Skywiremob.prepareVisor();

        Skywiremob.prepareVPNClient();
        Skywiremob.shakeHands();
        String tunIP = Skywiremob.tunip();
        String tunGateway = Skywiremob.tunGateway();
        boolean encrypt = Skywiremob.vpnEncrypt();
        Skywiremob.printString("TUN IP: " + tunIP);
        Skywiremob.printString("TUN GATEWAY: " + tunGateway);
        Skywiremob.printString("ENCRYPT: " + encrypt);

        Skywiremob.waitForVisorToStop();
        //kywiremob
        //var visor = Skywiremob./p
        //Skywiremob.runVisor();


        /*
         * Code you want to run on the thread goes here
         */
    }
}