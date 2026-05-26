#[cfg(target_os = "macos")]
mod platform {
    use objc2::{
        runtime::{AnyClass, NSObjectProtocol},
        ClassType,
    };
    use objc2_app_kit::{
        NSAutoresizingMaskOptions, NSColor, NSGlassEffectView, NSGlassEffectViewStyle, NSWindow,
    };
    use objc2_foundation::{NSOperatingSystemVersion, NSProcessInfo};
    use tauri::{Manager, Runtime, WebviewWindow};

    const LIQUID_GLASS_MAJOR_VERSION: isize = 26;

    #[derive(serde::Serialize)]
    #[serde(rename_all = "camelCase", tag = "status")]
    pub enum NativeGlassStatus {
        Applied { effect: &'static str },
        Unsupported { reason: &'static str },
    }

    pub async fn apply_to_label<R: Runtime>(
        app: tauri::AppHandle<R>,
        label: String,
    ) -> Result<NativeGlassStatus, String> {
        let window = app
            .get_webview_window(&label)
            .ok_or_else(|| format!("Window '{label}' was not found."))?;
        apply_to_window(window).await
    }

    pub fn apply_to_window_now<R: Runtime>(
        window: &WebviewWindow<R>,
    ) -> Result<NativeGlassStatus, String> {
        let ns_window = window
            .ns_window()
            .map_err(|error| format!("Resolve native window failed: {error}"))?;
        unsafe { apply_to_ns_window(ns_window.cast()) }
    }

    async fn apply_to_window<R: Runtime>(
        window: WebviewWindow<R>,
    ) -> Result<NativeGlassStatus, String> {
        let (sender, receiver) = tokio::sync::oneshot::channel();
        let scheduled_window = window.clone();
        scheduled_window
            .run_on_main_thread(move || {
                let result = apply_to_window_now(&window);
                let _ = sender.send(result);
            })
            .map_err(|error| format!("Schedule native glass setup failed: {error}"))?;
        receiver
            .await
            .map_err(|_| "Native glass setup ended before returning a result.".to_string())?
    }

    unsafe fn apply_to_ns_window(ns_window: *mut NSWindow) -> Result<NativeGlassStatus, String> {
        if ns_window.is_null() {
            return Err("Native window pointer is null.".to_string());
        }
        if !supports_liquid_glass(NSProcessInfo::processInfo().operatingSystemVersion()) {
            return Ok(NativeGlassStatus::Unsupported {
                reason: "macOS 26 or newer is required for NSGlassEffectView.",
            });
        }

        let window = unsafe { &*ns_window };
        let content_view = window
            .contentView()
            .ok_or_else(|| "Native window does not have a content view.".to_string())?;
        if content_view.isKindOfClass(NSGlassEffectView::class()) {
            return Ok(NativeGlassStatus::Applied {
                effect: "NSGlassEffectView.clear",
            });
        }
        let glass_view = NSGlassEffectView::new(
            objc2::MainThreadMarker::new()
                .ok_or_else(|| "Native glass setup must run on the main thread.".to_string())?,
        );
        let autoresizing_mask = NSAutoresizingMaskOptions::ViewWidthSizable
            | NSAutoresizingMaskOptions::ViewHeightSizable;

        window.setOpaque(false);
        window.setBackgroundColor(Some(&NSColor::clearColor()));
        glass_view.setFrame(content_view.frame());
        glass_view.setAutoresizingMask(autoresizing_mask);
        glass_view.setStyle(NSGlassEffectViewStyle::Clear);
        glass_view.setTintColor(None);
        glass_view.setCornerRadius(0.0);
        window.setContentView(Some(&glass_view));
        content_view.setFrame(glass_view.bounds());
        content_view.setAutoresizingMask(autoresizing_mask);
        glass_view.setContentView(Some(&content_view));

        Ok(NativeGlassStatus::Applied {
            effect: "NSGlassEffectView.clear",
        })
    }

    fn supports_liquid_glass(version: NSOperatingSystemVersion) -> bool {
        is_macos_26_or_newer(version) && AnyClass::get(c"NSGlassEffectView").is_some()
    }

    fn is_macos_26_or_newer(version: NSOperatingSystemVersion) -> bool {
        version.majorVersion >= LIQUID_GLASS_MAJOR_VERSION
    }

    #[cfg(test)]
    mod tests {
        use super::*;

        #[test]
        fn liquid_glass_requires_macos_26() {
            assert!(!is_macos_26_or_newer(NSOperatingSystemVersion {
                majorVersion: 25,
                minorVersion: 9,
                patchVersion: 9,
            }));
            assert!(is_macos_26_or_newer(NSOperatingSystemVersion {
                majorVersion: 26,
                minorVersion: 0,
                patchVersion: 0,
            }));
        }
    }
}

#[cfg(not(target_os = "macos"))]
mod platform {
    use tauri::Runtime;

    #[derive(serde::Serialize)]
    #[serde(rename_all = "camelCase", tag = "status")]
    pub enum NativeGlassStatus {
        Unsupported { reason: &'static str },
    }

    pub async fn apply_to_label<R: Runtime>(
        _app: tauri::AppHandle<R>,
        _label: String,
    ) -> Result<NativeGlassStatus, String> {
        Ok(NativeGlassStatus::Unsupported {
            reason: "Native Liquid Glass is only available on macOS.",
        })
    }
}

#[cfg(target_os = "macos")]
pub use platform::apply_to_window_now;
pub use platform::{apply_to_label, NativeGlassStatus};
