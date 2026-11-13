#pragma once

namespace stockbot {

bool httpFailureIsRetryable(int http_status, bool transport_ok);

}  // namespace stockbot
