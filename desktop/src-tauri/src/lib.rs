//! The desktop app (implementation plan §2.4, requirements §6.6): the H5 in
//! a Tauri 2 window. Its WebView's origin (tauri://localhost) is not the
//! API's, so the H5 signs in as an APP client with bearer tokens
//! (web/h5/src/lib/native.ts) and keeps the refresh token in the system's
//! secure storage through the three commands below.

const SERVICE: &str = "vip.astras.exchange";
const ACCOUNT: &str = "refresh_token";

fn entry() -> Result<keyring::Entry, String> {
    keyring::Entry::new(SERVICE, ACCOUNT).map_err(|e| e.to_string())
}

/// The stored refresh token, or none.
#[tauri::command]
fn refresh_token_get() -> Result<Option<String>, String> {
    match entry()?.get_password() {
        Ok(token) => Ok(Some(token)),
        Err(keyring::Error::NoEntry) => Ok(None),
        Err(e) => Err(e.to_string()),
    }
}

/// Stores the refresh token, replacing the previous one.
#[tauri::command]
fn refresh_token_set(token: String) -> Result<(), String> {
    entry()?.set_password(&token).map_err(|e| e.to_string())
}

/// Forgets the refresh token (sign-out, revoked session).
#[tauri::command]
fn refresh_token_clear() -> Result<(), String> {
    match entry()?.delete_credential() {
        Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
        Err(e) => Err(e.to_string()),
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![refresh_token_get, refresh_token_set, refresh_token_clear])
        .run(tauri::generate_context!())
        .expect("error while running the desktop app");
}
