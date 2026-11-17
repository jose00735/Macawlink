package com.macawlink

import android.app.Application
import com.macawlink.session.SessionHub
import com.macawlink.settings.SettingsStore

class MacawlinkApp : Application() {
    override fun onCreate() {
        super.onCreate()
        SessionHub.settings.value = SettingsStore(this).load()
    }
}
