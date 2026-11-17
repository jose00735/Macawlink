package com.macawlink.session

import com.macawlink.settings.StreamSettings
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.update

/** Process-wide state shared by the UI, the service and the PC link. */
object SessionHub {
    enum class Phase { STOPPED, WAITING_PC, STREAMING }

    data class LinkState(
        val phase: Phase = Phase.STOPPED,
        val pcName: String? = null,
        val width: Int = 0,
        val height: Int = 0,
        val fps: Double = 0.0,
        val kbps: Int = 0,
        val audioOn: Boolean = false,
        val error: String? = null,
    )

    /** Single source of truth for settings: the UI and SET_CONFIG from the PC both write here. */
    val settings = MutableStateFlow(StreamSettings())
    val state = MutableStateFlow(LinkState())

    fun update(f: (LinkState) -> LinkState) = state.update(f)
}
