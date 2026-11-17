package com.macawlink.ui

import android.Manifest
import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import com.macawlink.service.StreamService

/**
 * Home screen. Phase 1: permissions + Start/Stop.
 * Planned screens: Settings (resolution, fps, codec, bitrate, sample rate, channels, Opus bitrate,
 * AEC/NS/AGC, lens, mirror, presets), Pairing (QR scan), Preview.
 */
class MainActivity : ComponentActivity() {
    private val permissions = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { granted ->
        if (granted.values.all { it }) ContextCompat.startForegroundService(this, Intent(this, StreamService::class.java))
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            MaterialTheme {
                Column(Modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                    Text("Macawlink", style = MaterialTheme.typography.headlineMedium)
                    Text("Plug the phone in by USB and start Macawlink on your PC.")
                    Button(onClick = {
                        permissions.launch(arrayOf(Manifest.permission.CAMERA, Manifest.permission.RECORD_AUDIO, Manifest.permission.POST_NOTIFICATIONS))
                    }) { Text("Start streaming") }
                    Button(onClick = { stopService(Intent(this@MainActivity, StreamService::class.java)) }) { Text("Stop") }
                }
            }
        }
    }
}
