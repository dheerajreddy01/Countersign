/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  // Emit a self-contained server bundle (.next/standalone) for a small
  // production image. See web/Dockerfile.
  output: "standalone",
};

// Runtime configuration is read from the environment at request time, not here:
//   CONSOLE_API_URL — point case endpoints at the Go console API (reserved).
//   AI_SERVICE_URL  — POST /api/score proxies to the Python AI service.
// Reading process.env in the route handlers keeps these runtime-settable in the
// container rather than baked in at build time.

export default nextConfig;
