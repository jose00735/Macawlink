package com.macawlink.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import com.macawlink.R
import com.macawlink.service.StreamService
import com.macawlink.session.SessionHub
import com.macawlink.session.SessionHub.Phase
import com.macawlink.settings.SettingsStore
import com.macawlink.settings.StreamSettings

private val Cyan = Color(0xFF00F0FF)
private val Magenta = Color(0xFFFF2BD6)
private val Violet = Color(0xFF7B2CFF)
private val Deep = Color(0xFF0B0420)
private val Panel = Color(0xFF170C36)
private val Line = Color(0xFF2C1B5E)
private val Dim = Color(0xFF9A93C9)
private val Ok = Color(0xFF00F0A8)
private val Warn = Color(0xFFFFC21A)
private val Mono = FontFamily.Monospace

class MainActivity : ComponentActivity() {
    private lateinit var store: SettingsStore

    private val permissions = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { granted ->
        if (granted[Manifest.permission.CAMERA] == true) StreamService.start(this)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        store = SettingsStore(this)
        setContent {
            MaterialTheme(colorScheme = darkColorScheme(primary = Cyan, secondary = Magenta, background = Deep, surface = Panel)) {
                Home(
                    onStart = ::startStreaming,
                    onStop = { StreamService.stop(this) },
                    onSettings = { s -> store.save(s); SessionHub.settings.value = s },
                )
            }
        }
        handleAutostart(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleAutostart(intent)
    }

    /** The PC opens us with `--ez autostart true` when the phone is plugged in. */
    private fun handleAutostart(intent: Intent?) {
        if (intent?.getBooleanExtra("autostart", false) == true && SessionHub.state.value.phase == Phase.STOPPED) startStreaming()
    }

    private fun startStreaming() {
        val needed = buildList {
            add(Manifest.permission.CAMERA)
            add(Manifest.permission.RECORD_AUDIO)
            if (Build.VERSION.SDK_INT >= 33) add(Manifest.permission.POST_NOTIFICATIONS)
        }.filter { ContextCompat.checkSelfPermission(this, it) != PackageManager.PERMISSION_GRANTED }
        if (needed.isEmpty()) StreamService.start(this) else permissions.launch(needed.toTypedArray())
    }
}

@Composable
private fun Home(onStart: () -> Unit, onStop: () -> Unit, onSettings: (StreamSettings) -> Unit) {
    val state by SessionHub.state.collectAsState()
    val s by SessionHub.settings.collectAsState()
    // Teleprompter from TeleCyborg on the PC: full screen next to the lens, until closed.
    val prompter by SessionHub.prompter.collectAsState()
    var closedFor by remember { mutableStateOf("") }
    var promptCam by remember { mutableStateOf(false) }
    val p = prompter
    if (p != null && state.phase == Phase.STREAMING && closedFor != p.id + p.track) {
        PrompterView(p, state.width, state.height, promptCam, onCamera = { promptCam = it }, onClose = { closedFor = p.id + p.track })
        return
    }
    Box(
        Modifier.fillMaxSize().background(
            Brush.verticalGradient(listOf(Color(0xFF1B0B3A), Deep, Color(0xFF05020F)))
        )
    ) {
        Column(
            Modifier.fillMaxSize().safeDrawingPadding().verticalScroll(rememberScrollState()).padding(horizontal = 18.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            Header()
            StatusCard(state)
            val on = state.phase != Phase.STOPPED
            Button(
                onClick = if (on) onStop else onStart,
                modifier = Modifier.fillMaxWidth().height(56.dp).shadow(16.dp, RoundedCornerShape(14.dp), spotColor = if (on) Magenta else Cyan),
                shape = RoundedCornerShape(14.dp),
                colors = ButtonDefaults.buttonColors(containerColor = if (on) Color(0xFF3A0A33) else Color(0xFF07343A), contentColor = if (on) Magenta else Cyan),
            ) { Text(if (on) "STOP" else "START STREAMING", fontFamily = Mono, fontWeight = FontWeight.Bold, letterSpacing = 2.sp) }
            Text(
                "Plug the phone into your PC by USB (USB debugging on), or pick your PC under Wi-Fi. " +
                    "Hold the phone in landscape — the picture is turned upright automatically.",
                color = Dim, fontSize = 13.sp,
            )
            if (p != null && state.phase == Phase.STREAMING) {
                Button(onClick = { closedFor = "" }, modifier = Modifier.fillMaxWidth(), shape = RoundedCornerShape(14.dp),
                    colors = ButtonDefaults.buttonColors(containerColor = Color(0xFF07343A), contentColor = Cyan)) {
                    Text("OPEN TELEPROMPTER · ${p.title}", fontFamily = Mono, maxLines = 1)
                }
                if (s.video.lens != "front") Text("Read from the phone with the front camera: switch Lens to front.", color = Warn, fontSize = 13.sp)
            }
            var showPreview by remember { mutableStateOf(false) }
            if (state.phase == Phase.STREAMING) {
                Toggle("Show preview on the phone", showPreview) { showPreview = it }
                if (showPreview) CameraPreview(state.width, state.height)
            }
            WifiCard(onConnect = { if (state.phase == Phase.STOPPED) onStart() }) { title, content -> Card(title, content) }
            Presets(s, onSettings)
            VideoCard(s, onSettings)
            AudioCard(s, onSettings)
            Spacer(Modifier.height(24.dp))
        }
        PairingDialog()
    }
}

@Composable
private fun Header() {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Image(painterResource(R.drawable.ic_launcher_foreground), null, Modifier.size(56.dp))
        Text("MACAW", fontFamily = Mono, fontWeight = FontWeight.Bold, fontSize = 22.sp, letterSpacing = 3.sp, color = Color.White)
        Text("LINK", fontFamily = Mono, fontWeight = FontWeight.Bold, fontSize = 22.sp, letterSpacing = 3.sp, color = Cyan)
    }
}

@Composable
private fun Card(title: String, content: @Composable () -> Unit) {
    Column(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Panel.copy(alpha = 0.85f))
            .border(1.dp, Line, RoundedCornerShape(14.dp)).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(title.uppercase(), color = Magenta, fontFamily = Mono, fontSize = 12.sp, letterSpacing = 3.sp, fontWeight = FontWeight.SemiBold)
        content()
    }
}

@Composable
private fun StatusCard(st: SessionHub.LinkState) {
    val (color, text) = when (st.phase) {
        Phase.STOPPED -> Dim to "Stopped"
        Phase.WAITING_PC -> Warn to "Waiting for your PC…"
        Phase.CONNECTING -> Warn to "Connecting over Wi-Fi…"
        Phase.PAIRING -> Magenta to "Pairing — compare the code"
        Phase.STREAMING -> Ok to "Streaming to ${st.pcName ?: "PC"} · ${st.transport}"
    }
    Card("Status") {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(12.dp).shadow(8.dp, CircleShape, spotColor = color).clip(CircleShape).background(color))
            Spacer(Modifier.width(10.dp))
            Text(text, color = Color.White, fontFamily = Mono, fontSize = 15.sp)
        }
        if (st.phase == Phase.STREAMING) {
            Text(
                "${st.width}×${st.height} · ${"%.0f".format(st.fps)} fps · ${"%.1f".format(st.kbps / 1000.0)} Mbps · mic ${if (st.audioOn) "on" else "off"}",
                color = Cyan, fontFamily = Mono, fontSize = 13.sp,
            )
            if (st.targetKbps > 0) Text("Bitrate target ${"%.1f".format(st.targetKbps / 1000.0)} Mbps", color = Dim, fontFamily = Mono, fontSize = 12.sp)
            if (st.thermal >= 2) Text("Phone is warm — quality reduced to protect it", color = Warn, fontSize = 13.sp)
        }
        st.error?.let { Text(it, color = Magenta, fontSize = 13.sp) }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun <T> Choice(label: String, options: List<Pair<T, String>>, selected: T, onPick: (T) -> Unit) {
    Text(label, color = Dim, fontSize = 13.sp)
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        options.forEach { (value, name) ->
            FilterChip(
                selected = value == selected, onClick = { onPick(value) },
                label = { Text(name, fontFamily = Mono, fontSize = 12.sp) },
                colors = FilterChipDefaults.filterChipColors(
                    selectedContainerColor = Cyan.copy(alpha = 0.18f), selectedLabelColor = Cyan, labelColor = Color.White,
                ),
                border = FilterChipDefaults.filterChipBorder(true, value == selected, borderColor = Line, selectedBorderColor = Cyan),
            )
        }
    }
}

@Composable
private fun Toggle(label: String, value: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(label, color = Color.White, modifier = Modifier.weight(1f))
        Switch(value, onChange, colors = SwitchDefaults.colors(checkedThumbColor = Cyan, checkedTrackColor = Cyan.copy(alpha = 0.3f)))
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun Presets(s: StreamSettings, onSettings: (StreamSettings) -> Unit) {
    Card("Presets") {
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            StreamSettings.PRESETS.forEach { (name, p) ->
                val active = p.video.width == s.video.width && p.video.fps == s.video.fps && p.audio.sampleRate == s.audio.sampleRate
                FilterChip(
                    selected = active,
                    onClick = {
                        onSettings(p.copy(
                            video = p.video.copy(lens = s.video.lens, mirror = s.video.mirror),
                            audio = p.audio.copy(gainDb = s.audio.gainDb),
                        ))
                    },
                    label = { Text(name, fontFamily = Mono) },
                    colors = FilterChipDefaults.filterChipColors(selectedContainerColor = Magenta.copy(alpha = 0.2f), selectedLabelColor = Magenta, labelColor = Color.White),
                    border = FilterChipDefaults.filterChipBorder(true, active, borderColor = Violet, selectedBorderColor = Magenta),
                )
            }
        }
    }
}

@Composable
private fun VideoCard(s: StreamSettings, onSettings: (StreamSettings) -> Unit) {
    val v = s.video
    fun set(nv: StreamSettings.Video) = onSettings(s.copy(video = nv))
    Card("Video") {
        Choice("Resolution", StreamSettings.RESOLUTIONS.map { it to "${it.second}p" }, v.width to v.height) { set(v.copy(width = it.first, height = it.second)) }
        Choice("Frame rate", StreamSettings.FRAME_RATES.map { it to "$it fps" }, v.fps) { set(v.copy(fps = it)) }
        Choice("Bitrate", StreamSettings.BITRATES.map { it to if (it == 0) "Auto" else "${it / 1000.0} Mb" }, v.bitrateKbps) { set(v.copy(bitrateKbps = it)) }
        Choice("Codec", listOf("h264" to "H.264", "hevc" to "HEVC"), v.codec) { set(v.copy(codec = it)) }
        Choice("Camera", listOf("back" to "Back", "front" to "Front"), v.lens) { set(v.copy(lens = it, mirror = it == "front")) }
        var zoom by remember(v.zoom) { mutableFloatStateOf(v.zoom.toFloat()) }
        Text("Zoom ${"%.1f".format(zoom)}×", color = Dim, fontSize = 13.sp)
        Slider(zoom, { zoom = it }, valueRange = 1f..5f, onValueChangeFinished = { set(v.copy(zoom = zoom.toDouble())) },
            colors = SliderDefaults.colors(thumbColor = Cyan, activeTrackColor = Cyan))
        Toggle("Torch", v.torch) { set(v.copy(torch = it)) }
        Choice("Focus", listOf("auto" to "Auto", "manual" to "Manual"), v.focusMode) { set(v.copy(focusMode = it)) }
        if (v.focusMode == "manual") {
            var focus by remember(v.focus) { mutableFloatStateOf(v.focus.toFloat()) }
            Text("Focus ${if (focus < 0.05f) "far" else if (focus > 0.95f) "closest" else "%.0f%%".format(focus * 100)}", color = Dim, fontSize = 13.sp)
            Slider(focus, { focus = it }, valueRange = 0f..1f, onValueChangeFinished = { set(v.copy(focus = focus.toDouble())) },
                colors = SliderDefaults.colors(thumbColor = Cyan, activeTrackColor = Cyan))
        }
        var ev by remember(v.exposure) { mutableFloatStateOf(v.exposure.toFloat()) }
        Text("Exposure ${"%+.1f".format(ev)} EV", color = Dim, fontSize = 13.sp)
        Slider(ev, { ev = Math.round(it * 3) / 3f }, valueRange = -2f..2f, onValueChangeFinished = { set(v.copy(exposure = ev.toDouble())) },
            colors = SliderDefaults.colors(thumbColor = Cyan, activeTrackColor = Cyan))
        Choice("White balance", StreamSettings.WHITE_BALANCES.map { it to it.replaceFirstChar { c -> c.uppercase() } }, v.whiteBalance) { set(v.copy(whiteBalance = it)) }
        Toggle("Lock exposure", v.aeLock) { set(v.copy(aeLock = it)) }
        Toggle("Lock white balance", v.awbLock) { set(v.copy(awbLock = it)) }
        Toggle("Stabilization", v.stabilization) { set(v.copy(stabilization = it)) }
        Toggle("Mirror (on PC)", v.mirror) { set(v.copy(mirror = it)) }
    }
}

@Composable
private fun AudioCard(s: StreamSettings, onSettings: (StreamSettings) -> Unit) {
    val a = s.audio
    fun set(na: StreamSettings.Audio) = onSettings(s.copy(audio = na))
    Card("Microphone") {
        Toggle("Send microphone", a.enabled) { set(a.copy(enabled = it)) }
        Choice("Sample rate", StreamSettings.SAMPLE_RATES.map { it to "${it / 1000.0} kHz".replace(".0 ", " ") }, a.sampleRate) { set(a.copy(sampleRate = it)) }
        Choice("Channels", listOf(1 to "Mono", 2 to "Stereo"), a.channels) { set(a.copy(channels = it)) }
        Choice("Packet size", listOf(10 to "10 ms", 20 to "20 ms"), a.frameMs) { set(a.copy(frameMs = it)) }
        Toggle("Echo cancellation", a.aec) { set(a.copy(aec = it)) }
        Toggle("Noise suppression", a.ns) { set(a.copy(ns = it)) }
        Toggle("Auto gain", a.agc) { set(a.copy(agc = it)) }
    }
}
