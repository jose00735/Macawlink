package com.macawlink.capture

import android.annotation.SuppressLint
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.MediaRecorder
import android.media.audiofx.AcousticEchoCanceler
import android.media.audiofx.AutomaticGainControl
import android.media.audiofx.NoiseSuppressor
import com.macawlink.settings.StreamSettings
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * Captures the mic in [frameMs] chunks. Output goes to AudioEncoder (Opus) or straight to
 * the transport as PCM s16le (USB only).
 */
class AudioPipeline(private val onPcm: (pcm: ByteArray, ptsUs: Long) -> Unit) {
    private var record: AudioRecord? = null
    private var job: Job? = null

    @SuppressLint("MissingPermission") // RECORD_AUDIO is requested before the service starts
    fun start(scope: CoroutineScope, s: StreamSettings.Audio) {
        val channelMask = if (s.channels == 2) AudioFormat.CHANNEL_IN_STEREO else AudioFormat.CHANNEL_IN_MONO
        val frameBytes = s.sampleRate * s.channels * 2 * s.frameMs / 1000
        val minBuf = AudioRecord.getMinBufferSize(s.sampleRate, channelMask, AudioFormat.ENCODING_PCM_16BIT)
        val source = if (s.aec || s.ns) MediaRecorder.AudioSource.VOICE_COMMUNICATION else MediaRecorder.AudioSource.MIC

        val r = AudioRecord(source, s.sampleRate, channelMask, AudioFormat.ENCODING_PCM_16BIT, maxOf(minBuf, frameBytes * 4))
        if (s.aec && AcousticEchoCanceler.isAvailable()) AcousticEchoCanceler.create(r.audioSessionId)?.enabled = true
        if (s.ns && NoiseSuppressor.isAvailable()) NoiseSuppressor.create(r.audioSessionId)?.enabled = true
        if (s.agc && AutomaticGainControl.isAvailable()) AutomaticGainControl.create(r.audioSessionId)?.enabled = true
        record = r
        r.startRecording()

        job = scope.launch(Dispatchers.IO) {
            val buf = ByteArray(frameBytes)
            while (isActive) {
                var read = 0
                while (read < frameBytes) {
                    val n = r.read(buf, read, frameBytes - read)
                    if (n <= 0) break
                    read += n
                }
                // Same monotonic clock as the camera timestamps, for A/V sync on the PC.
                onPcm(buf.copyOf(read), System.nanoTime() / 1000)
            }
        }
    }

    fun stop() {
        job?.cancel()
        runCatching { record?.stop() }
        record?.release()
        record = null
    }
}
