package com.macawlink.capture

import android.annotation.SuppressLint
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.MediaRecorder
import android.media.audiofx.AcousticEchoCanceler
import android.media.audiofx.AutomaticGainControl
import android.media.audiofx.NoiseSuppressor
import android.os.Process
import android.util.Log
import com.macawlink.settings.StreamSettings
import kotlin.concurrent.thread

/** Captures the mic as PCM s16le in fixed frames and hands them to [onPcm]. */
class AudioCapture(private val onPcm: (pcm: ByteArray, ptsUs: Long) -> Unit) {
    @Volatile private var running = false
    private var worker: Thread? = null
    private var record: AudioRecord? = null
    private val effects = mutableListOf<android.media.audiofx.AudioEffect>()

    @SuppressLint("MissingPermission") // RECORD_AUDIO checked by the service
    fun start(s: StreamSettings.Audio): Boolean {
        stop()
        val channelMask = if (s.channels == 2) AudioFormat.CHANNEL_IN_STEREO else AudioFormat.CHANNEL_IN_MONO
        val frameBytes = s.sampleRate * s.channels * 2 * s.frameMs / 1000
        val minBuf = AudioRecord.getMinBufferSize(s.sampleRate, channelMask, AudioFormat.ENCODING_PCM_16BIT)
        if (minBuf <= 0) return false
        // VOICE_COMMUNICATION engages the phone's own echo/noise processing on most devices.
        val source = if (s.aec || s.ns || s.agc) MediaRecorder.AudioSource.VOICE_COMMUNICATION else MediaRecorder.AudioSource.MIC
        val r = try {
            AudioRecord(source, s.sampleRate, channelMask, AudioFormat.ENCODING_PCM_16BIT, maxOf(minBuf, frameBytes * 8))
        } catch (e: Exception) {
            Log.w(TAG, "AudioRecord", e); return false
        }
        if (r.state != AudioRecord.STATE_INITIALIZED) { r.release(); return false }
        val sid = r.audioSessionId
        if (AcousticEchoCanceler.isAvailable()) AcousticEchoCanceler.create(sid)?.also { it.enabled = s.aec; effects += it }
        if (NoiseSuppressor.isAvailable()) NoiseSuppressor.create(sid)?.also { it.enabled = s.ns; effects += it }
        if (AutomaticGainControl.isAvailable()) AutomaticGainControl.create(sid)?.also { it.enabled = s.agc; effects += it }
        record = r
        r.startRecording()
        running = true
        worker = thread(name = "mk-audio") {
            Process.setThreadPriority(Process.THREAD_PRIORITY_URGENT_AUDIO)
            val buf = ByteArray(frameBytes)
            while (running) {
                var read = 0
                while (read < frameBytes && running) {
                    val n = r.read(buf, read, frameBytes - read)
                    if (n <= 0) break
                    read += n
                }
                if (read == frameBytes) onPcm(buf.copyOf(), System.nanoTime() / 1000)
                else if (running) Thread.sleep(2)
            }
        }
        Log.i(TAG, "audio ${s.sampleRate} Hz x${s.channels}, ${s.frameMs} ms frames")
        return true
    }

    fun stop() {
        running = false
        worker?.join(500)
        worker = null
        effects.forEach { runCatching { it.release() } }
        effects.clear()
        record?.let { runCatching { it.stop() }; it.release() }
        record = null
    }

    private companion object { const val TAG = "MacawlinkAudio" }
}
