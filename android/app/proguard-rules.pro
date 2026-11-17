# kotlinx.serialization: keep generated serializers for our protocol/settings classes
-keepattributes *Annotation*, InnerClasses
-keep,includedescriptorclasses class com.macawlink.**$$serializer { *; }
-keepclassmembers class com.macawlink.** { *** Companion; }
-keepclasseswithmembers class com.macawlink.** { kotlinx.serialization.KSerializer serializer(...); }
