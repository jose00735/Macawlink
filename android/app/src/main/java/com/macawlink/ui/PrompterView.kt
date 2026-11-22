package com.macawlink.ui

import android.view.Surface
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInParent
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.macawlink.protocol.PrompterScript
import com.macawlink.session.SessionHub
import kotlin.math.abs

private val Read = Color(0xFFE9FFF1)
private val Past = Color(0xFF7C8FA0)
private val Accent = Color(0xFF00F0FF)
private val Amber = Color(0xFFFFB000)
private val Red = Color(0xFFFF4D4D)

/** Which screen edge the front camera is on, from the display rotation. */
internal enum class LensEdge { TOP, LEFT, RIGHT, BOTTOM }

internal fun lensEdge(rotation: Int): LensEdge = when (rotation) {
    Surface.ROTATION_90 -> LensEdge.LEFT   // turned counter-clockwise: the top of the phone is on the left
    Surface.ROTATION_270 -> LensEdge.RIGHT
    Surface.ROTATION_180 -> LensEdge.BOTTOM
    else -> LensEdge.TOP
}

/** "**word**" → bold. */
internal fun stressed(t: String): AnnotatedString = buildAnnotatedString {
    val parts = t.split("**")
    parts.forEachIndexed { i, s -> if (i % 2 == 1) withStyle(SpanStyle(fontWeight = FontWeight.Bold, color = Color.White)) { append(s) } else append(s) }
}

/**
 * Full-screen teleprompter fed by TeleCyborg on the PC. The text sits next to the front lens so
 * your eyes stay near the camera; it is drawn on the screen only and never enters the stream.
 * Tap: play/pause · swipe up/down: next/previous paragraph · ✕: back to settings.
 */
@Composable
fun PrompterView(script: PrompterScript, width: Int, height: Int, showCamera: Boolean, onCamera: (Boolean) -> Unit, onClose: () -> Unit) {
    val view = LocalView.current
    DisposableEffect(Unit) {
        view.keepScreenOn = true
        onDispose { view.keepScreenOn = false }
    }
    val st by SessionHub.prompterState.collectAsState()
    @Suppress("DEPRECATION")
    val edge = lensEdge(view.display?.rotation ?: Surface.ROTATION_0)
    val paras = remember(script) { script.blocks.withIndex().filter { it.value.k == "para" }.map { it.index } }
    val tops = remember(script) { mutableStateMapOf<Int, Pair<Int, Int>>() } // block index → top, height
    val scroll = rememberScrollState()
    val current = remember { mutableIntStateOf(0) }
    val density = LocalDensity.current

    BoxWithConstraints(
        Modifier.fillMaxSize().background(Color.Black)
            .pointerInput(Unit) { detectTapGestures(onTap = { SessionHub.prompterKeys.tryEmit("toggle") }) }
            .pointerInput(Unit) {
                var acc = 0f
                detectVerticalDragGestures(onDragEnd = {
                    if (abs(acc) > 80) SessionHub.prompterKeys.tryEmit(if (acc < 0) "next" else "prev")
                    acc = 0f
                }) { _, d -> acc += d }
            },
    ) {
        if (showCamera && width > 0 && height > 0) {
            Box(Modifier.fillMaxSize().graphicsLayer { alpha = 0.55f }) { CameraPreview(width, height) }
        }
        val landscape = maxWidth > maxHeight
        val tail = maxHeight * 0.8f
        val focusPx = with(density) { (maxHeight * if (edge == LensEdge.BOTTOM) 0.62f else 0.22f).toPx() }
        // follow the PC's position, moving smoothly between updates
        LaunchedEffect(script) {
            while (true) {
                withFrameNanos { now ->
                    val s = SessionHub.prompterState.value ?: return@withFrameNanos
                    val (pos, at) = s
                    if (pos.id.isNotEmpty() && pos.id != script.id) return@withFrameNanos
                    val k = paras.getOrNull(pos.para) ?: return@withFrameNanos
                    current.intValue = pos.para
                    val (top, h) = tops[k] ?: return@withFrameNanos
                    val frac = (pos.frac + if (pos.playing) pos.speed * (now - at) / 1e9 else 0.0).coerceIn(0.0, 1.0)
                    val target = (top + frac * h - focusPx).toInt().coerceAtLeast(0)
                    val delta = target - scroll.value
                    if (delta != 0) scroll.dispatchRawDelta(if (abs(delta) > 400) delta.toFloat() else delta * 0.35f)
                }
            }
        }
        val colWidth = if (landscape) maxWidth * 0.58f else maxWidth
        val align = when {
            !landscape -> Alignment.TopCenter
            edge == LensEdge.LEFT -> Alignment.TopStart
            edge == LensEdge.RIGHT -> Alignment.TopEnd
            else -> Alignment.TopCenter
        }
        Box(Modifier.fillMaxSize().safeDrawingPadding(), contentAlignment = align) {
            Box(
                Modifier.width(colWidth).fillMaxHeight()
                    .background(Brush.verticalGradient(listOf(Color(0xE6000000), Color(0xB3000000), Color(0x66000000))))
                    .graphicsLayer { if (script.mirror) scaleX = -1f },
            ) {
                Column(
                    Modifier.fillMaxSize().verticalScroll(scroll, enabled = false).padding(horizontal = 18.dp),
                    verticalArrangement = Arrangement.spacedBy(14.dp),
                ) {
                    Box(Modifier.height(with(density) { focusPx.toDp() }))
                    val size = (30 * script.fontScale).coerceIn(18.0, 64.0).sp
                    script.blocks.forEachIndexed { i, b ->
                        val mod = Modifier.fillMaxWidth().onGloballyPositioned { tops[i] = it.positionInParent().y.toInt() to it.size.height }
                        when (b.k) {
                            "sec" -> Text(b.t.uppercase(), mod, color = if (script.track == "answer") Amber else Accent, fontSize = 13.sp, fontFamily = FontFamily.Monospace, letterSpacing = 2.sp)
                            "cue" -> Text("› " + b.t, mod, color = Amber, fontSize = (size.value * 0.5f).sp)
                            else -> {
                                val pi = paras.indexOf(i)
                                Text(stressed(b.t), mod, fontSize = size, lineHeight = (size.value * 1.35f).sp,
                                    color = when { pi < current.intValue -> Past; pi == current.intValue -> Read; else -> Read.copy(alpha = 0.72f) })
                            }
                        }
                    }
                    Box(Modifier.height(tail))
                }
                // focus marker
                Box(Modifier.offset(y = with(density) { focusPx.toDp() }).width(6.dp).height(28.dp).background(Accent))
            }
        }
        // status strip on the side away from the lens
        val pos = st?.first
        Row(
            Modifier.align(if (edge == LensEdge.BOTTOM) Alignment.TopCenter else Alignment.BottomCenter).safeDrawingPadding().padding(8.dp)
                .clip(RoundedCornerShape(10.dp)).background(Color(0xCC0B0420)).padding(horizontal = 12.dp, vertical = 6.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically,
        ) {
            val alertColor = when (pos?.alert) { "behind", "wrap" -> Amber; "miss" -> Red; "ahead" -> Accent; else -> Past }
            Text(if (pos?.playing == true) "▶" else "❚❚", color = Accent, fontSize = 14.sp)
            Text(pos?.clock ?: "", color = Read, fontFamily = FontFamily.Monospace, fontSize = 14.sp)
            if (pos != null && pos.wpm > 0) Text("${pos.wpm} wpm", color = Past, fontFamily = FontFamily.Monospace, fontSize = 13.sp)
            val msg = when (pos?.alert) { "behind" -> "behind · ${pos.hint}"; "miss" -> "skip a paragraph"; "ahead" -> "slow down"; "wrap" -> "wrap up"; else -> "" }
            if (msg.isNotEmpty()) Text(msg, color = alertColor, fontSize = 13.sp)
            TextButton(onClick = { onCamera(!showCamera) }) { Text(if (showCamera) "Hide camera" else "Camera", color = Past, fontSize = 12.sp) }
            TextButton(onClick = onClose) { Text("✕", color = Past) }
        }
    }
}
