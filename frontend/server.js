const http = require("http");

const INTERNAL_API_URL = process.env.INTERNAL_API_URL || "http://backend:8080";

const server = http.createServer(async (req, res) => {
  try {
    // Meneruskan Cookie dari browser ke Backend API saat proses SSR
    const apiRes = await fetch(`${INTERNAL_API_URL}/api/health`, {
      headers: {
        Cookie: req.headers.cookie || "",
      },
    });
    const data = await apiRes.json();

    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    res.end(`
      <!DOCTYPE html>
      <html>
        <head><title>Database Management Platform</title></head>
        <body>
          <h1>Database Management Platform (SSR)</h1>
          <p>Status koneksi ke Backend API: <strong>${data.status}</strong> (${data.service})</p>
        </body>
      </html>
    `);
  } catch (err) {
    res.writeHead(502, { "Content-Type": "text/plain" });
    res.end("Gagal menghubungi Backend API dari SSR Server");
  }
});

server.listen(3000, () => {
  console.log("SSR Frontend berjalan pada :3000");
});