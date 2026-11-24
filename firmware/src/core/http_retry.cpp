#include "http_retry.h"

namespace stockbot {

bool httpFailureIsRetryable(int http_status, bool transport_ok) {
  if (!transport_ok) {
    return true;
  }
  return http_status >= 500 || http_status == 429;
}

}  // namespace stockbot
