package com.skycoin.skywire

import com.skycoin.skywire.wallet.WalletMesh
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test
import java.net.Proxy
import java.net.URI

/** Wallet calls to .dmsg nodes: the hex key rewritten to its DNS label, and only mesh hosts proxied. */
class WalletMeshTest {

    // The deployment's Skycoin node, as config gen names it (cmd/skywire-cli/commands/config/gen.go).
    private val hex = "039a6d1e3c237f5f05b78ec19e9f31a007f84835d7ef1e812876102281d1db74c1"

    // What cipher.PubKey.DNSLabel gives for the same key.
    private val label = "aong2hr4en7v6bnxr3az5hzruad7qsbv27xr5ajioyicfaor3n2mc"

    @Test
    fun hexKeyBecomesTheBase32Label() {
        assertEquals(label, WalletMesh.dnsLabel(hex))
        assertEquals("http://$label.dmsg:6420", WalletMesh.normalize(" http://$hex.dmsg:6420 "))
        assertEquals("http://$label.dmsg/api", WalletMesh.normalize("http://${hex.uppercase()}.DMSG/api"))
        assertEquals("https://node.skycoin.com", WalletMesh.normalize("https://node.skycoin.com"))
    }

    // Why the rewrite: a 66-character label is no host to a URL parser (RFC 1035 2.3.4).
    @Test
    fun onlyTheLabelFormParses() {
        assertNull("http://$hex.dmsg:6420".toHttpUrlOrNull())
        assertNotNull("http://$label.dmsg:6420".toHttpUrlOrNull())
    }

    @Test
    fun onlyMeshHostsGoThroughTheResolver() {
        val viaMesh = WalletMesh.proxySelector.select(URI("http://$label.dmsg:6420/api/v1/health")).single()
        assertEquals(Proxy.Type.SOCKS, viaMesh.type())
        assertEquals(Proxy.NO_PROXY, WalletMesh.proxySelector.select(URI("https://node.skycoin.com/api")).single())
    }
}
