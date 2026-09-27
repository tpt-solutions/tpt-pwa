// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! tpt-cortex-shell: the optional desktop installer ([spec.txt](../spec.txt)
//! §7). It bundles three things into one seamless install:
//!
//! 1. the built PWA (served from `pwa/dist`),
//! 2. the Go `cortex-daemon` as a Tauri sidecar (`binaries/cortex-daemon`,
//!    target-suffixed at bundle time), and
//! 3. the `tpt://` protocol registration via the deep-link plugin.
//!
//! With the daemon running locally, the PWA inside the window detects
//! `ws://127.0.0.1:9911` and lights up Path A automatically -- the PWA's own
//! capability negotiation does the work; the shell only ships the runtime.

use tauri_plugin_shell::process::CommandEvent;
use tauri_plugin_shell::ShellExt;

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_deep_link::init())
        .setup(|app| {
            spawn_daemon_sidecar(app.handle().clone());
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tpt-cortex-shell");
}

/// Start the bundled cortex-daemon and keep the app alive about it: on exit
/// or failure we surface the daemon's output in the log; the PWA's fallback
/// path covers any gap automatically.
fn spawn_daemon_sidecar(app: tauri::AppHandle) {
    let handle = app.clone();
    tauri::async_runtime::spawn(async move {
        let shell = handle.shell();
        match shell.sidecar("cortex-daemon") {
            Ok(command) => match command.spawn() {
                Ok((mut rx, _child)) => {
                    while let Some(event) = rx.recv().await {
                        match event {
                            CommandEvent::Stdout(line) => {
                                log_stdout(&String::from_utf8_lossy(&line))
                            }
                            CommandEvent::Stderr(line) => {
                                log_stdout(&String::from_utf8_lossy(&line))
                            }
                            CommandEvent::Terminated(status) => {
                                log_stdout(format!("daemon exited: {status:?}").as_str())
                            }
                            _ => {}
                        }
                    }
                }
                Err(err) => log_stdout(format!("daemon spawn failed: {err}").as_str()),
            },
            Err(err) => log_stdout(format!("daemon sidecar not found: {err}").as_str()),
        }
    });
}

fn log_stdout(message: &str) {
    println!("[cortex-daemon] {message}");
}

#[cfg(test)]
mod tests {
    //! Baseline tests: pin the bundle configuration (sidecar daemon and the
    //! tpt:// scheme) so packaging regressions fail `cargo test`, not the
    //! installer.

    #[test]
    fn tauri_conf_registers_tpt_scheme_and_daemon_sidecar() {
        let conf: serde_json::Value = serde_json::from_str(include_str!("../tauri.conf.json")).expect("tauri.conf.json parses");
        let schemes = conf["plugins"]["deep-link"]["desktop"]["schemes"]
            .as_array()
            .expect("deep-link schemes configured");
        assert_eq!(schemes[0], "tpt");
        let sidecar = conf["bundle"]["externalBin"]
            .as_array()
            .expect("externalBin configured");
        assert_eq!(sidecar[0], "binaries/cortex-daemon");
    }
}
