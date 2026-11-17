package com.macawlink.ui

import android.view.SurfaceHolder
import android.view.SurfaceView
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import com.macawlink.protocol.Proto
import com.macawlink.session.SessionHub
import com.macawlink.settings.WifiStore
import com.macawlink.transport.PcDiscovery

private val Cyan = Color(0xFF00F0FF)
private val Magenta = Color(0xFFFF2BD6)
private val Line = Color(0xFF2C1B5E)
private val Dim = Color(0xFF9A93C9)
private val Mono = FontFamily.Monospace

/** Wi-Fi: pick a PC found on the network (or type its address); paired PCs reconnect by themselves. */
@Composable
fun WifiCard(onConnect: () -> Unit, card: @Composable (String, @Composable () -> Unit) -> Unit) {
    val ctx = LocalContext.current
    val store = remember { WifiStore(ctx) }
    val discovery = remember { PcDiscovery(ctx) }
    val target by SessionHub.wifiTarget.collectAsState()
    val found by discovery.found.collectAsState()
    var manual by remember { mutableStateOf("") }
    var paired by remember { mutableStateOf(store.paired()) }

    DisposableEffect(Unit) {
        discovery.start()
        onDispose { discovery.stop() }
    }
    fun choose(t: SessionHub.WifiTarget?) {
        store.saveTarget(t)
        SessionHub.wifiTarget.value = t
        if (t != null) onConnect()
    }

    card("Wi-Fi") {
        if (target != null) {
            Text("Connected to ${target!!.name} · ${target!!.host}", color = Cyan, fontFamily = Mono, fontSize = 13.sp)
            TextButton(onClick = { choose(null) }) { Text("Stop using Wi-Fi", color = Magenta) }
        } else {
            Text("Pick your PC (it must run Macawlink on the same network):", color = Dim, fontSize = 13.sp)
        }
        if (found.isEmpty()) Text("Searching…", color = Dim, fontSize = 13.sp, fontFamily = Mono)
        found.forEach { pc ->
            val isPaired = store.pairedFor(pc.shortFp) != null
            Row(
                Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).border(1.dp, if (pc == target) Cyan else Line, RoundedCornerShape(10.dp))
                    .clickable { choose(pc) }.padding(12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f)) {
                    Text(pc.name, color = Color.White, fontFamily = Mono)
                    Text("${pc.host} · code ${pc.shortFp}", color = Dim, fontSize = 12.sp, fontFamily = Mono)
                }
                Text(if (isPaired) "PAIRED" else "PAIR", color = if (isPaired) Cyan else Magenta, fontFamily = Mono, fontSize = 12.sp)
            }
        }
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedTextField(manual, { manual = it.trim() }, label = { Text("PC address (e.g. 192.168.1.20)") },
                singleLine = true, modifier = Modifier.weight(1f))
            Button(onClick = {
                val host = manual.substringBefore(':')
                val port = manual.substringAfter(':', "").toIntOrNull() ?: Proto.WIFI_PORT
                if (host.isNotBlank()) choose(SessionHub.WifiTarget(host, port, host))
            }, colors = ButtonDefaults.buttonColors(containerColor = Color(0xFF07343A), contentColor = Cyan)) { Text("Go") }
        }
        if (paired.isNotEmpty()) {
            Text("Paired PCs", color = Dim, fontSize = 13.sp)
            paired.forEach { pc ->
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text(pc.name, color = Color.White, fontFamily = Mono, modifier = Modifier.weight(1f))
                    TextButton(onClick = { store.forget(pc.fp); paired = store.paired() }) { Text("Forget", color = Magenta) }
                }
            }
        }
    }
}

/** Pairing: both screens show the same 6 digits unless someone is in the middle. */
@Composable
fun PairingDialog() {
    val prompt by SessionHub.pairing.collectAsState()
    val p = prompt ?: return
    AlertDialog(
        onDismissRequest = {},
        containerColor = Color(0xFF170C36),
        title = { Text("Pair with ${p.pcName}?", color = Color.White) },
        text = {
            Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.fillMaxWidth()) {
                Text("${p.code.take(3)} ${p.code.drop(3)}", color = Cyan, fontFamily = Mono, fontWeight = FontWeight.Bold,
                    fontSize = 40.sp, letterSpacing = 6.sp, modifier = Modifier.padding(vertical = 12.dp))
                Text("Check that your PC shows exactly this code, then accept on both.", color = Dim, fontSize = 13.sp)
            }
        },
        confirmButton = { TextButton(onClick = { p.answer.complete(true) }) { Text("Codes match", color = Cyan) } },
        dismissButton = { TextButton(onClick = { p.answer.complete(false) }) { Text("Cancel", color = Magenta) } },
    )
}

/** Live preview from the camera stream (a second output of the capture session). */
@Composable
fun CameraPreview(width: Int, height: Int) {
    if (width <= 0 || height <= 0) return
    AndroidView(
        modifier = Modifier.fillMaxWidth().aspectRatio(width.toFloat() / height).clip(RoundedCornerShape(14.dp))
            .border(1.dp, Cyan, RoundedCornerShape(14.dp)).background(Color.Black),
        factory = { ctx ->
            SurfaceView(ctx).apply {
                holder.setFixedSize(width, height)
                holder.addCallback(object : SurfaceHolder.Callback {
                    override fun surfaceCreated(h: SurfaceHolder) { SessionHub.previewSurface.value = h.surface }
                    override fun surfaceChanged(h: SurfaceHolder, f: Int, w: Int, hh: Int) = Unit
                    override fun surfaceDestroyed(h: SurfaceHolder) { SessionHub.previewSurface.value = null }
                })
            }
        },
        update = { it.holder.setFixedSize(width, height) },
    )
    DisposableEffect(Unit) { onDispose { SessionHub.previewSurface.value = null } }
}
