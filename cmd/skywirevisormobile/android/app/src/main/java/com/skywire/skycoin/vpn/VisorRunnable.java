package com.skywire.skycoin.vpn;

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
    private String RemotePK;
    private String Passcode;

    public VisorRunnable(Context context, MainActivity activity, String remotePK, String passcode) {
        this.context = context;
        this.activity = activity;
        this.RemotePK = remotePK;
        this.Passcode = passcode;
    }

    public void stopVisor() {
        String err = Skywiremob.stopVisor();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
        }
    }

    private void showToast(String text) {
        activity.runOnUiThread(new Runnable() {
            public void run() {
                Toast.makeText(activity, text, Toast.LENGTH_SHORT).show();
            }
        });
    }

    @Override
    public void run() {
        android.os.Process.setThreadPriority(android.os.Process.THREAD_PRIORITY_BACKGROUND);

        String err = Skywiremob.prepareVisor();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }
        Skywiremob.printString("Prepared visor");

        err = Skywiremob.waitVisorReady();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }

        err = Skywiremob.prepareVPNClient(this.RemotePK, this.Passcode);
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }
        Skywiremob.printString("Prepared VPN client");

        err = Skywiremob.shakeHands();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }

        err = Skywiremob.startListeningUDP();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }

        err = Skywiremob.serveVPN();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }

        try {
            Skywiremob.printString("VPN IS READY, SLEEPING...");
            Thread.sleep(1 * 1000 * 10);
        } catch (InterruptedException e) {
            e.printStackTrace();
        }

        activity.runOnUiThread(new Runnable() {
            public void run() {
                activity.startVPNService();
            }
        });

        /*err = Skywiremob.waitForVisorToStop();
        if (!err.isEmpty()) {
            Skywiremob.printString(err);
            showToast(err);
            return;
        }*/
    }
}