#pragma once

namespace stockbot {

inline constexpr const char kPortalWifiNameLabel[] = "Your home Wi-Fi name";
inline constexpr const char kPortalWifiPasswordLabel[] = "Wi-Fi password";
inline constexpr const char kPortalConnectingMessage[] = "Connecting to your Wi-Fi...";
inline constexpr const char kPortalSetupHtml[] =
    R"HTML(<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Scanner Setup</title></head><body><h1>Scanner Setup</h1>
<form method="POST" action="/save"><label>Your home Wi-Fi name<input name="ssid" required></label><br>
<label>Wi-Fi password<input name="pass" type="password"></label><br>
<button type="submit">Save</button></form></body></html>)HTML";

}  // namespace stockbot
