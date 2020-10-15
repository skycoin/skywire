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
        long code = Skywiremob.stopVisor();
        if (code != Skywiremob.ErrCodeNoError) {
            String err = "Failed to stop visor: " + ((Long)code).toString();
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

        long code = Skywiremob.prepareVisor();
        if (code != Skywiremob.ErrCodeNoError) {
            String err = "Failed to prepare visor: " + ((Long)code).toString();
            Skywiremob.printString(err);
            showToast(err);
            return;
        }
        Skywiremob.printString("Prepared visor");

        code = Skywiremob.waitVisorReady();
        if (code != Skywiremob.ErrCodeNoError) {
            String err = "Failed to start visor: " + ((Long)code).toString();
            Skywiremob.printString(err);
            showToast(err);
            return;
        }

        code = Skywiremob.prepareVPNClient(this.RemotePK, this.Passcode);
        if (code != Skywiremob.ErrCodeNoError) {
            String err = "Failed to prepare VPN client: " + ((Long)code).toString();
            Skywiremob.printString(err);
            showToast(err);
            return;
        }
        Skywiremob.printString("Prepared VPN client");

        code = Skywiremob.shakeHands();
        if (code != Skywiremob.ErrCodeNoError) {
            String err = "Failed to perform client/server handshake: " + ((Long)code).toString();
            Skywiremob.printString(err);
            showToast(err);
            return;
        }

        code = Skywiremob.startListeningUDP();
        if (code != Skywiremob.ErrCodeNoError) {
            String err = "Failed to start listening UDP: " + ((Long)code).toString();
            Skywiremob.printString(err);
            showToast(err);
            return;
        }

        code = Skywiremob.serveVPN();
        if (code != Skywiremob.ErrCodeNoError) {
            String err = "Failed to serve VPN: " + ((Long)code).toString();
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