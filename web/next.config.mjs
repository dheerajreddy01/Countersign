/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  // The console API (Go) proxies here in a full deployment. Point the browser and
  // route handlers at it with CONSOLE_API_URL; unset, the app serves its own fixtures.
  env: {
    CONSOLE_API_URL: process.env.CONSOLE_API_URL ?? "",
    AI_SERVICE_URL: process.env.AI_SERVICE_URL ?? "",
  },
};

export default nextConfig;
