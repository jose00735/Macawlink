package com.macawlink

import android.app.Application
import com.macawlink.session.SessionHub
import com.macawlink.settings.SettingsStore
import com.macawlink.settings.WifiStore

class MacawlinkApp : Application() {
    override fun onCreate() {
        super.onCreate()
        SessionHub.settings.value = SettingsStore(this).load()
        val ws = WifiStore(this)
        SessionHub.wifiTarget.value = ws.lastTarget()
        SessionHub.linkMode.value = ws.linkMode
    }
}
