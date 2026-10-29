// SPDX-License-Identifier: Unlicense OR MIT

package org.gioui;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.LinkAddress;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.os.Build;
import android.os.Handler;
import android.os.HandlerThread;

import java.net.InetAddress;

// GioNetwork follows the network under the app and reports it to Go
// (app.WatchNetwork). From API 31 it follows the best network that is not a
// VPN, so a VPN on top does not hide a move underneath. Before that it
// follows the default network and Go skips the VPN reports.
final class GioNetwork {
	private static ConnectivityManager.NetworkCallback callback;
	private static HandlerThread thread;

	static synchronized void start(Context ctx) {
		if (callback != null || Build.VERSION.SDK_INT < 24) {
			return;
		}
		final ConnectivityManager cm = (ConnectivityManager)ctx.getSystemService(Context.CONNECTIVITY_SERVICE);
		if (cm == null) {
			return;
		}
		callback = new ConnectivityManager.NetworkCallback() {
			@Override public void onAvailable(Network n) {
				report(cm, n, null, null);
			}

			@Override public void onLinkPropertiesChanged(Network n, LinkProperties lp) {
				report(cm, n, lp, null);
			}

			@Override public void onCapabilitiesChanged(Network n, NetworkCapabilities caps) {
				report(cm, n, null, caps);
			}

			@Override public void onLost(Network n) {
				onNetworkLost(n.toString());
			}
		};
		thread = new HandlerThread("GioNetwork");
		thread.start();
		Handler h = new Handler(thread.getLooper());
		try {
			if (Build.VERSION.SDK_INT >= 31) {
				NetworkRequest req = new NetworkRequest.Builder()
					.addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
					.addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
					.build();
				cm.registerBestMatchingNetworkCallback(req, callback, h);
			} else if (Build.VERSION.SDK_INT >= 26) {
				cm.registerDefaultNetworkCallback(callback, h);
			} else {
				cm.registerDefaultNetworkCallback(callback);
			}
		} catch (SecurityException e) {
			// The app did not import gioui.org/app/permission/networkstate.
			callback = null;
			thread.quitSafely();
			thread = null;
		}
	}

	static synchronized void stop(Context ctx) {
		if (callback == null) {
			return;
		}
		ConnectivityManager cm = (ConnectivityManager)ctx.getSystemService(Context.CONNECTIVITY_SERVICE);
		try {
			cm.unregisterNetworkCallback(callback);
		} catch (IllegalArgumentException e) {
			// Not registered.
		}
		callback = null;
		thread.quitSafely();
		thread = null;
	}

	private static void report(ConnectivityManager cm, Network n, LinkProperties lp, NetworkCapabilities caps) {
		if (lp == null) {
			lp = cm.getLinkProperties(n);
		}
		if (caps == null) {
			caps = cm.getNetworkCapabilities(n);
		}
		StringBuilder addrs = new StringBuilder();
		String ifname = "";
		if (lp != null) {
			if (lp.getInterfaceName() != null) {
				ifname = lp.getInterfaceName();
			}
			for (LinkAddress la : lp.getLinkAddresses()) {
				InetAddress a = la.getAddress();
				if (a.isLinkLocalAddress() || a.isLoopbackAddress() || a.isAnyLocalAddress()) {
					continue;
				}
				if (addrs.length() > 0) {
					addrs.append(',');
				}
				addrs.append(a.getHostAddress());
			}
		}
		int metered = -1;
		boolean vpn = false;
		if (caps != null) {
			metered = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED) ? 0 : 1;
			vpn = caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN);
		}
		onNetwork(n.toString(), ifname, addrs.toString(), metered, vpn);
	}

	static private native void onNetwork(String id, String ifname, String addrs, int metered, boolean vpn);
	static private native void onNetworkLost(String id);
}
