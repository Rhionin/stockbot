fn main() {
    if std::env::var("IDF_PATH").is_err() {
        let version = std::env::var("ESP_IDF_VERSION").unwrap_or_else(|_| "v5.3.2".to_string());
        if let Some(idf_path) = idf_path_from_espressif(&version) {
            std::env::set_var("IDF_PATH", &idf_path);
        }
    }
    embuild::espidf::sysenv::output();
}

fn idf_path_from_espressif(esp_idf_version: &str) -> Option<String> {
    let home = std::env::var("HOME").ok()?;
    let root = std::path::Path::new(&home).join(".espressif");
    let candidates = [
        root.join("esp-idf").join(esp_idf_version),
        root.join("esp-idf").join(esp_idf_version).join("esp-idf"),
        root.join("esp-idf"),
    ];
    for path in candidates {
        if path.join("tools/idf.py").exists() {
            return Some(path.to_string_lossy().into_owned());
        }
    }
    None
}