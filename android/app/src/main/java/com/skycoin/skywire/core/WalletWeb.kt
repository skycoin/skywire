package com.skycoin.skywire.core

/**
 * Where the core serves skycoin-web, the wallet the wallet tab shows. 8002
 * is skycoin-web's skywire default, one above skychat's 8001.
 */
object WalletWeb {
    const val ADDR = "127.0.0.1:8002"
    const val URL = "http://$ADDR/"
}
