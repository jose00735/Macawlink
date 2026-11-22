package com.macawlink.ui

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import com.android.resources.ScreenOrientation
import com.macawlink.protocol.PrompterBlock
import com.macawlink.protocol.PrompterScript
import com.macawlink.protocol.PrompterState
import com.macawlink.session.SessionHub
import org.junit.Rule
import org.junit.Test

/** Renders the phone teleprompter (fed by TeleCyborg) on the JVM, phone held in landscape. */
class PrompterViewTest {
    @get:Rule
    val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_6.copy(orientation = ScreenOrientation.LANDSCAPE), theme = "android:Theme.Material.NoActionBar")

    @Test
    fun landscape() {
        val script = PrompterScript(id = "pitch", title = "Terrific pitch", blocks = listOf(
            PrompterBlock("sec", "Opening"),
            PrompterBlock("para", "Hi, I'm **Jose Acosta**, a Customer Success Manager based in Colombia."),
            PrompterBlock("cue", "smile"),
            PrompterBlock("para", "At Netskope I moved an account from below fifty to seventy percent utilisation."),
            PrompterBlock("sec", "Fortinet"),
            PrompterBlock("para", "Before that I spent two years at Fortinet as the escalation point for enterprise cases."),
        ))
        SessionHub.prompter.value = script
        SessionHub.prompterState.value = PrompterState(id = "pitch", para = 0, frac = 0.3, playing = true, wpm = 140, clock = "0:12 / 1:30", alert = "behind", hint = "speed up to 152 wpm") to System.nanoTime()
        paparazzi.snapshot { PrompterView(script, 1280, 720, showCamera = false, onCamera = {}, onClose = {}) }
    }
}
