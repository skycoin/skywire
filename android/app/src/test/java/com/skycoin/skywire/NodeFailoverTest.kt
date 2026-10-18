package com.skycoin.skywire

import com.skycoin.skywire.wallet.NodeFailover
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.IOException

/** Each test uses its own hosts: a failed node is remembered across instances. */
class NodeFailoverTest {

    private val asked = mutableListOf<String>()

    /** Answers [codes] by host, or drops the connection for a host it does not know. */
    private fun client(nodes: List<String>, codes: Map<String, Int>) = OkHttpClient.Builder()
        .addInterceptor(NodeFailover(nodes))
        .addInterceptor { chain ->
            val url = chain.request().url
            asked += url.toString()
            val code = codes[url.host] ?: throw IOException("connection refused")
            Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(code).message("x")
                .body("{}".toResponseBody("application/json".toMediaType())).build()
        }
        .build()

    private fun get(c: OkHttpClient, url: String) = c.newCall(Request.Builder().url(url).build()).execute().use { it.code }

    @Test
    fun aServerErrorMovesOnLikeADeadNode() {
        val c = client(listOf("https://a1.test", "https://b1.test"), mapOf("a1.test" to 503, "b1.test" to 200))
        assertEquals(200, get(c, "https://a1.test/api/v1/health"))
        assertEquals(listOf("https://a1.test/api/v1/health", "https://b1.test/api/v1/health"), asked)
    }

    @Test
    fun throttlingMovesOnToo() {
        val c = client(listOf("https://a2.test", "https://b2.test"), mapOf("a2.test" to 429, "b2.test" to 200))
        assertEquals(200, get(c, "https://a2.test/api/v1/health"))
    }

    @Test
    fun theLastNodesErrorIsTheAnswer() {
        val c = client(listOf("https://a3.test", "https://b3.test"), mapOf("b3.test" to 500))
        assertEquals(500, get(c, "https://a3.test/api/v1/health"))
    }

    @Test
    fun eachNodeKeepsItsOwnPath() {
        val c = client(listOf("https://a4.test/sky/", "https://b4.test/node"), mapOf("b4.test" to 200))
        get(c, "https://a4.test/sky/api/v1/outputs?addrs=x,y")
        assertEquals("https://b4.test/node/api/v1/outputs?addrs=x,y", asked.last())
    }
}
