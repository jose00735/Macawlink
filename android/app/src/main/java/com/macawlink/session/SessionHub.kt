package com.macawlink.session

import android.view.Surface
import com.macawlink.settings.StreamSettings
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.update

/** Process-wide state shared by the UI, the service and the PC link. */
object SessionHub {
    enum class Phase { STOPPED, WAITING_PC, CONNECTING, PAIRING, STREAMING }

    /** AUTO: USB whenever the cable carries a PC session, Wi-Fi otherwise (switches by itself). */
    enum class LinkMode { AUTO, USB, WIFI }

    data class LinkState(
        val phase: Phase = Phase.STOPPED,
        val transport: String = "",
        val pcName: String? = null,
        val width: Int = 0,
        val height: Int = 0,
        val fps: Double = 0.0,
        val kbps: Int = 0,
        val targetKbps: Int = 0, // adaptive bitrate target (auto mode)
        val thermal: Int = 0,
        val audioOn: Boolean = false,
        val error: String? = null,
    )

    /** A PC to reach over Wi-Fi (from mDNS discovery or typed in). */
    data class WifiTarget(val host: String, val port: Int, val name: String, val shortFp: String = "")

    /** Shown while pairing: the user compares [code] with the PC screen. */
    data class PairPrompt(val pcName: String, val code: String, val answer: CompletableDeferred<Boolean>)

    /** Single source of truth for settings: the UI and SET_CONFIG from the PC both write here. */
    val settings = MutableStateFlow(StreamSettings())
    val state = MutableStateFlow(LinkState())
    val wifiTarget = MutableStateFlow<WifiTarget?>(null)
    val pairing = MutableStateFlow<PairPrompt?>(null)
    val previewSurface = MutableStateFlow<Surface?>(null)
    val linkMode = MutableStateFlow(LinkMode.AUTO)

    /** Teleprompter text from the PC (TeleCyborg), or null when none is shown. */
    val prompter = MutableStateFlow<com.macawlink.protocol.PrompterScript?>(null)
    /** Latest position with the time it arrived (System.nanoTime), for smooth motion. */
    val prompterState = MutableStateFlow<Pair<com.macawlink.protocol.PrompterState, Long>?>(null)
    /** Taps on the phone's prompter, sent to the PC as PROMPTER_KEY. */
    val prompterKeys = kotlinx.coroutines.flow.MutableSharedFlow<String>(extraBufferCapacity = 16)

    fun update(f: (LinkState) -> LinkState) = state.update(f)
}
