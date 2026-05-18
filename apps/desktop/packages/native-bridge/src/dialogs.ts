import { LogicalPosition, LogicalSize } from "@tauri-apps/api/dpi";
import { WebviewWindow } from "@tauri-apps/api/webviewWindow";
import { Effect, EffectState, getCurrentWindow } from "@tauri-apps/api/window";

export type NativeDialogWindowOptions = Readonly<{
  label: string;
  title: string;
  route: string;
  params: Readonly<Record<string, string>>;
  initialWidth?: number;
  initialHeight?: number;
  maximizable?: boolean;
  resizable?: boolean;
}>;

export type NativeDialogContentSize = Readonly<{
  width: number;
  height: number;
}>;

export async function openNativeDialogWindow(options: NativeDialogWindowOptions): Promise<void> {
  const url = routeWithParams(options.route, options.params);
  const label = options.label.startsWith("native-dialog-") ? options.label : `native-dialog-${options.label}`;
  await new Promise<void>((resolve, reject) => {
    const window = new WebviewWindow(label, {
      center: true,
      closable: true,
      decorations: true,
      focus: true,
      height: options.initialHeight ?? 360,
      hiddenTitle: true,
      maximizable: options.maximizable ?? false,
      parent: getCurrentWindow(),
      resizable: options.resizable ?? false,
      shadow: true,
      title: options.title,
      titleBarStyle: "overlay",
      trafficLightPosition: new LogicalPosition(20, 18),
      transparent: true,
      url,
      visible: true,
      width: options.initialWidth ?? 520,
      windowEffects: {
        effects: [Effect.UnderWindowBackground, Effect.Acrylic],
        radius: 18,
        state: EffectState.Active,
      },
    });
    window
      .once("tauri://created", () => {
        resolve();
      })
      .catch(reject);
    window
      .once("tauri://error", (event) => {
        reject(new Error(String(event.payload)));
      })
      .catch(reject);
  });
}

export async function fitCurrentWindowToContent(size: NativeDialogContentSize): Promise<void> {
  const logicalSize = new LogicalSize(
    Math.max(1, Math.ceil(size.width)),
    Math.max(1, Math.ceil(size.height)),
  );
  const window = getCurrentWindow();
  await window.setMinSize(null);
  await window.setMaxSize(null);
  await window.setSize(logicalSize);
  await window.setMinSize(logicalSize);
  await window.setMaxSize(logicalSize);
}

function routeWithParams(route: string, params: Readonly<Record<string, string>>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    search.set(key, value);
  }
  const suffix = search.size > 0 ? `?${search.toString()}` : "";
  return `${route}${suffix}`;
}
