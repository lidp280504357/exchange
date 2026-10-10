// ApiError carries the unified error body (requirements §7.1): a stable
// code, a message, details and the trace ID to quote in support requests.
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details: Record<string, unknown> = {},
    public traceId = "",
  ) {
    super(message);
    this.name = "ApiError";
  }
}

type ErrorBody = { code?: string; message?: string; details?: Record<string, unknown>; trace_id?: string };

/**
 * errorFrom builds an ApiError from a parsed error body; anything else (a
 * proxy's HTML page, an empty body) keeps only the HTTP status.
 */
export function errorFrom(status: number, statusText: string, body: unknown): ApiError {
  const b: ErrorBody = typeof body === "object" && body !== null ? (body as ErrorBody) : {};
  const code = b.code ?? (status === 429 ? "COMMON_RATE_LIMITED" : status >= 500 ? "COMMON_UNAVAILABLE" : "COMMON_INTERNAL");
  return new ApiError(status, code, b.message ?? statusText, b.details ?? {}, b.trace_id ?? "");
}

/** toApiError reads the error from a response whose body is still unread. */
export async function toApiError(res: Response): Promise<ApiError> {
  let body: unknown;
  try {
    body = await res.clone().json();
  } catch {
    // not JSON
  }
  return errorFrom(res.status, res.statusText, body);
}

/**
 * retryServerErrors retries a failed query once when the server or the
 * network failed; a refusal (4xx: not eligible, unknown network, a 404) is
 * an answer, final.
 */
export function retryServerErrors(failures: number, error: unknown): boolean {
  if (error instanceof ApiError && error.status < 500) return false;
  return failures < 1;
}
