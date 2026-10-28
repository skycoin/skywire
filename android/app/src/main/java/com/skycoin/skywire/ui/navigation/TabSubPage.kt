package com.skycoin.skywire.ui.navigation

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.mutableStateOf

/**
 * True while a tab shows one of its own pages (a chat thread) rather than its
 * top screen. On a short screen the bottom bar then gives way, as it does for a
 * route pushed over a tab.
 */
val tabOnSubPage: State<Boolean> get() = subPage

private val subPage = mutableStateOf(false)

/** Marks the tab as on a sub-page while [on] is true, and clears it when the screen leaves. */
@Composable
fun TabSubPage(on: Boolean) {
    DisposableEffect(on) {
        subPage.value = on
        onDispose { subPage.value = false }
    }
}
