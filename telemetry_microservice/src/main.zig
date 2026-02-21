const std = @import("std");

const AGENT_PORT: u16 = 8080;
const POST_URL = "http://127.0.0.1:8080/ingest";

const Payload = struct {
    pid: ?u32,
    uptime: ?[]const u8,
    meminfo: ?[]const u8,
    kernel: ?[]const u8,
};

pub fn main() !void {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    var client: std.http.Client = .{ .allocator = allocator };
    defer client.deinit();

    while (true) {
        try runOnce(&client, allocator);
        std.Thread.sleep(5 * std.time.ns_per_s);
    }
}

fn runOnce(client: *std.http.Client, allocator: std.mem.Allocator) !void {
    const uptime = try runCmd(allocator, &.{ "cat", "/proc/uptime" });
    defer if (uptime) |u| allocator.free(u);

    const meminfo = try runCmd(allocator, &.{ "cat", "/proc/meminfo" });
    defer if (meminfo) |m| allocator.free(m);

    const kernel = try runCmd(allocator, &.{ "uname", "-r" });
    defer if (kernel) |k| allocator.free(k);

    const pid = try pidFromPort(allocator, AGENT_PORT);

    const payload = Payload{
        .pid = pid,
        .uptime = uptime,
        .meminfo = meminfo,
        .kernel = kernel,
    };

    try sendPostJsonFetch(client, allocator, POST_URL, payload);
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

fn pidFromPort(al: std.mem.Allocator, port: u16) !?u32 {
    var buf: [6]u8 = undefined;
    const arg = try std.fmt.bufPrint(&buf, ":{d}", .{port});

    const output = try runCmd(al, &.{ "lsof", "-ti", arg });
    defer if (output) |o| al.free(o);

    if (output == null) return null;

    const trimmed = std.mem.trim(u8, output.?, " \n\r\t");
    if (trimmed.len == 0) return null;

    var it = std.mem.splitScalar(u8, trimmed, '\n');
    const first = it.next().?;

    return try std.fmt.parseInt(u32, first, 10);
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
