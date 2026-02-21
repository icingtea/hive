const std = @import("std");

const Payload = struct {
    deployment_id: []const u8,
    agent_id: []const u8,
    pid: ?u32,
    uptime: ?[]const u8,
    meminfo: ?[]const u8,
    kernel: ?[]const u8,
    memory_bytes: ?u64,
    memory_limit_bytes: ?u64,
};

pub fn main() !void {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    const deployment_id = std.posix.getenv("HIVE_DEPLOYMENT_ID") orelse "unknown";
    const agent_id = std.posix.getenv("HIVE_AGENT_ID") orelse "unknown";
    const orchestrator_url = std.posix.getenv("HIVE_ORCHESTRATOR_URL") orelse "http://localhost:8181";

    const post_url = try std.fmt.allocPrint(allocator, "{s}/api/v1/ingest/heartbeat", .{orchestrator_url});
    defer allocator.free(post_url);

    var client: std.http.Client = .{ .allocator = allocator };
    defer client.deinit();

    while (true) {
        runOnce(&client, allocator, deployment_id, agent_id, post_url) catch |err| {
            std.debug.print("heartbeat error: {}\n", .{err});
        };
        std.Thread.sleep(5 * std.time.ns_per_s);
    }
}

fn runOnce(client: *std.http.Client, allocator: std.mem.Allocator, deployment_id: []const u8, agent_id: []const u8, post_url: []const u8) !void {
    const uptime = try runCmd(allocator, &.{ "cat", "/proc/uptime" });
    defer if (uptime) |u| allocator.free(u);

    const meminfo = try runCmd(allocator, &.{ "cat", "/proc/meminfo" });
    defer if (meminfo) |m| allocator.free(m);

    const kernel = try runCmd(allocator, &.{ "uname", "-r" });
    defer if (kernel) |k| allocator.free(k);

    // Find agent PID via pgrep python
    const pid = try pidFromPgrep(allocator, "python");

    var memory_bytes: ?u64 = null;
    var memory_limit_bytes: ?u64 = null;
    if (pid) |p| {
        const status_path = try std.fmt.allocPrint(allocator, "/proc/{d}/status", .{p});
        defer allocator.free(status_path);
        if (try runCmd(allocator, &.{ "cat", status_path })) |content| {
            defer allocator.free(content);
            if (parseProcStatusField(content, "VmRSS:")) |kb| {
                memory_bytes = kb * 1024;
            }
        }
    }

    // memory limit from cgroup (pod-level)
    if (try runCmd(allocator, &.{ "cat", "/sys/fs/cgroup/memory.max" })) |content| {
        defer allocator.free(content);
        const trimmed = std.mem.trim(u8, content, " \n\r\t");
        if (!std.mem.eql(u8, trimmed, "max")) {
            memory_limit_bytes = std.fmt.parseInt(u64, trimmed, 10) catch null;
        }
    }

    const payload = Payload{
        .deployment_id = deployment_id,
        .agent_id = agent_id,
        .pid = pid,
        .uptime = uptime,
        .meminfo = meminfo,
        .kernel = kernel,
        .memory_bytes = memory_bytes,
        .memory_limit_bytes = memory_limit_bytes,
    };

    try sendPostJsonFetch(client, allocator, post_url, payload);
}

fn runCmd(al: std.mem.Allocator, argv: []const []const u8) !?[]u8 {
    var child = std.process.Child.init(argv, al);
    child.stdout_behavior = .Pipe;
    child.stderr_behavior = .Ignore;

    try child.spawn();

    const stdout = child.stdout orelse return null;
    const out = try stdout.readToEndAlloc(al, 64 * 1024);

    const term = try child.wait();
    if (term.Exited != 0) {
        al.free(out);
        return null;
    }

    return out;
}

fn pidFromPgrep(al: std.mem.Allocator, name: []const u8) !?u32 {
    const output = try runCmd(al, &.{ "/usr/bin/pgrep", name });
    defer if (output) |o| al.free(o);

    if (output == null) return null;

    const trimmed = std.mem.trim(u8, output.?, " \n\r\t");
    if (trimmed.len == 0) return null;

    var it = std.mem.splitScalar(u8, trimmed, '\n');
    const first = it.next().?;

    return std.fmt.parseInt(u32, first, 10) catch null;
}

// Parse a kB field from /proc/<pid>/status (e.g. "VmRSS:   1234 kB")
fn parseProcStatusField(content: []const u8, field: []const u8) ?u64 {
    var lines = std.mem.splitScalar(u8, content, '\n');
    while (lines.next()) |line| {
        if (std.mem.startsWith(u8, line, field)) {
            const rest = std.mem.trim(u8, line[field.len..], " \t");
            var it = std.mem.splitScalar(u8, rest, ' ');
            if (it.next()) |num| {
                return std.fmt.parseInt(u64, num, 10) catch null;
            }
        }
    }
    return null;
}

fn sendPostJsonFetch(
    client: *std.http.Client,
    allocator: std.mem.Allocator,
    url: []const u8,
    payload: anytype,
) !void {
    var out: std.Io.Writer.Allocating = .init(allocator);
    try std.json.Stringify.value(payload, .{}, &out.writer);

    _ = try client.fetch(.{
        .method = .POST,
        .location = .{ .url = url },
        .headers = .{
            .content_type = .{ .override = "application/json" },
        },
        .payload = out.written(),
    });
}
