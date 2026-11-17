package com.macawlink.capture

import android.content.Context
import android.util.Size
import android.view.Surface
import androidx.camera.core.CameraSelector
import androidx.camera.core.Preview
import androidx.camera.core.resolutionselector.ResolutionSelector
import androidx.camera.core.resolutionselector.ResolutionStrategy
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.core.content.ContextCompat
import androidx.lifecycle.LifecycleOwner
import com.macawlink.settings.StreamSettings

/**
 * Binds CameraX so frames land directly on the encoder's input Surface (zero copy).
 * Lens switch, zoom and torch come from CAMERA_CONTROL messages.
 */
class CameraPipeline(private val context: Context, private val owner: LifecycleOwner) {
    private var provider: ProcessCameraProvider? = null
    private var camera: androidx.camera.core.Camera? = null

    fun bind(s: StreamSettings.Video, encoderSurface: Surface) {
        val future = ProcessCameraProvider.getInstance(context)
        future.addListener({
            val p = future.get().also { provider = it }
            val preview = Preview.Builder()
                .setResolutionSelector(
                    ResolutionSelector.Builder()
                        .setResolutionStrategy(
                            ResolutionStrategy(Size(s.width, s.height), ResolutionStrategy.FALLBACK_RULE_CLOSEST_HIGHER_THEN_LOWER),
                        ).build(),
                )
                .build()
            preview.setSurfaceProvider { req ->
                req.provideSurface(encoderSurface, ContextCompat.getMainExecutor(context)) { }
            }
            val selector = if (s.lens == "front") CameraSelector.DEFAULT_FRONT_CAMERA else CameraSelector.DEFAULT_BACK_CAMERA
            p.unbindAll()
            camera = p.bindToLifecycle(owner, selector, preview)
        }, ContextCompat.getMainExecutor(context))
    }

    fun setTorch(on: Boolean) { camera?.cameraControl?.enableTorch(on) }
    fun setZoom(ratio: Float) { camera?.cameraControl?.setZoomRatio(ratio) }
    fun unbind() { provider?.unbindAll() }
}
