package org.golang.example.bind;

import android.content.Context;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.os.Message;
import android.widget.Toast;

import java.io.IOException;
import java.io.OutputStream;
import java.io.PrintWriter;
import java.net.Socket;

import skywiremob.Skywiremob;

public class VisorRunnable implements Runnable {
    private Context context;
    private MainActivity activity;
    //private Handler activity;

    public VisorRunnable(Context context, MainActivity activity) {
        this.context = context;
        this.activity = activity;
    }

    public void stopVisor() {
        String err = Skywiremob.stopVisor();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
        }
    }

    private void showToast(String text) {
        /*Message msg = new Message();
        Bundle data =new Bundle();
        data.putString("text", text);
        msg.setData(data);
        activity.sendMessage(msg);*/

        activity.runOnUiThread(new Runnable() {
            public void run() {
                Toast.makeText(activity, text, Toast.LENGTH_SHORT).show();
            }
        });
    }

    @Override
    public void run() {
        android.os.Process.setThreadPriority(android.os.Process.THREAD_PRIORITY_BACKGROUND);

        Skywiremob.printString("INSIDE RUNNABLE");
        Skywiremob.prepareLogger();
        Skywiremob.printString("PREPARED LOGGER");
        String err = Skywiremob.prepareVisor();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }

        Skywiremob.printString("PREPARED VISOR");

        err = Skywiremob.prepareVPNClient();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }
        Skywiremob.printString("PREPARED VPN CLIENT");
        err = Skywiremob.shakeHands();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }
        Skywiremob.printString("SHOOK HANDS");

        err = Skywiremob.startListeningUDP();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }

        //Skywiremob.printDmsgServers();

        activity.runOnUiThread(new Runnable() {
            public void run() {
                activity.startVPNService();
            }
        });

        err = Skywiremob.waitForVisorToStop();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }
    }
}