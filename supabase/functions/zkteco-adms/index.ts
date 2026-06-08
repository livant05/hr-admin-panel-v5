import { serve } from "https://deno.land/std@0.168.0/http/server.ts";
import { createClient } from "https://esm.sh/@supabase/supabase-js@2";

const PLAIN = { "Content-Type": "text/plain; charset=utf-8" };

serve(async (req: Request) => {
  // ZKTeco devices don't send CORS preflight, but allow it for dashboard testing
  if (req.method === "OPTIONS") return new Response("ok", { status: 200 });

  const url = new URL(req.url);
  const sn = url.searchParams.get("SN") || url.searchParams.get("sn") || "UNKNOWN";
  const table = (url.searchParams.get("table") || "").toUpperCase();
  const path = url.pathname;

  const supabase = createClient(
    Deno.env.get("SUPABASE_URL") ?? "",
    Deno.env.get("SUPABASE_SERVICE_ROLE_KEY") ?? ""
  );

  // ── Device registration / heartbeat ──────────────────────────────────────
  if (req.method === "GET") {
    // Mark device as online
    await supabase.from("zkteco_devices").upsert(
      { serial_number: sn, last_seen: new Date().toISOString(), status: "online" },
      { onConflict: "serial_number" }
    );

    // Command polling endpoint — return empty (no pending commands)
    if (path.includes("getrequest")) {
      return new Response("OK", { headers: PLAIN });
    }

    // Registration response — tell device to push ATTLOG, no stamp filter
    const stamp = Math.floor(Date.now() / 1000);
    const body = [
      "GET OPTION",
      `ATTLOGStamp=${stamp}`,
      "OPERATORStamp=None",
      "ATTPHOTOStamp=None",
      "ErrorDelay=30",
      "Delay=10",
      "TransTimes=00:00;14:05",
      "TransInterval=1",
      "TransFlag=TransData AttLog",
      "TimeLimit=0",
      `BioDataStamp=${stamp}`,
      "OPTIONINFO Size=0",
    ].join("\n") + "\n";

    return new Response(body, { headers: PLAIN });
  }

  // ── Attendance / data push ────────────────────────────────────────────────
  if (req.method === "POST") {
    const body = await req.text();

    // Update device last_seen
    await supabase.from("zkteco_devices").upsert(
      { serial_number: sn, last_seen: new Date().toISOString(), status: "online" },
      { onConflict: "serial_number" }
    );

    // Only process ATTLOG records
    if (table !== "ATTLOG" && !body.includes("\t")) {
      return new Response("OK", { headers: PLAIN });
    }

    const lines = body.split(/\r?\n/).filter((l) => l.includes("\t"));
    let processed = 0;
    let unknown = 0;

    for (const line of lines) {
      const parts = line.trim().split("\t");
      if (parts.length < 4) continue;

      const pin = parts[0].trim();           // Employee PIN / Cédula
      const dateTime = parts[1].trim();      // YYYY-MM-DD HH:MM:SS
      const inOut = parseInt(parts[3]) || 0; // 0=in, 1=out, 4=OT-in, 5=OT-out

      const [date, timeRaw] = dateTime.split(" ");
      if (!date || !timeRaw) continue;
      const time = timeRaw.slice(0, 5); // HH:MM

      // Resolve employee by cedula
      const { data: emp } = await supabase
        .from("employees")
        .select("id, first_name, last_name, company_id, department")
        .eq("cedula", pin)
        .maybeSingle();

      if (!emp) {
        // Record unknown PIN for admin review
        await supabase.from("zkteco_unknown_pins").upsert(
          { pin, last_seen: new Date().toISOString(), serial_number: sn, attempts: 1 },
          { onConflict: "pin" }
        );
        unknown++;
        continue;
      }

      if (inOut === 0 || inOut === 4) {
        // Check-in or overtime-in
        const { error } = await supabase.from("attendance_logs").upsert(
          {
            company_id: emp.company_id,
            employee_id: emp.id,
            employee_name: `${emp.first_name} ${emp.last_name}`,
            department: emp.department || "",
            date,
            time_in: time,
            status: "present",
          },
          { onConflict: "employee_id,date" }
        );
        if (!error) processed++;
      } else if (inOut === 1 || inOut === 5) {
        // Check-out or overtime-out — only update time_out, preserve time_in
        const { error } = await supabase
          .from("attendance_logs")
          .update({ time_out: time })
          .eq("employee_id", emp.id)
          .eq("date", date);
        if (!error) processed++;
      }
    }

    return new Response(`OK: ${processed}`, { headers: PLAIN });
  }

  return new Response("OK", { headers: PLAIN });
});
