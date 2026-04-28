const net = require('net');
const path = require('path');
const Module = require('module');

const PROJECT_ROOT = process.cwd();
const PORT = 9876;

let sock = null;
let connected = false;
const eventQueue = [];

function connect() {
    sock = new net.Socket();
    sock.connect(PORT, 'localhost', () => {
        connected = true;
        process.stderr.write('Maplet: Connected to monitor.\n');
        while (eventQueue.length > 0) {
            trySend(eventQueue.shift());
        }
    });
    sock.on('error', () => {
        connected = false;
        setTimeout(connect, 1000);
    });
    sock.on('close', () => {
        connected = false;
        setTimeout(connect, 1000);
    });
}

function trySend(event) {
    if (connected && sock) {
        try {
            sock.write(JSON.stringify(event) + '\n');
        } catch (e) {
            connected = false;
            eventQueue.push(event);
        }
    } else {
        eventQueue.push(event);
    }
}

// serialize function arguments and truncate larger values
function _safeArgs(args) {
    const result = {};
    for (let i = 0; i < args.length; i++) {
        try {
            const v = args[i];
            if (v === null || v === undefined || typeof v === 'boolean' || typeof v === 'number') {
                result[i] = v;
            } else {
                const s = String(v);
                result[i] = s.length <= 120 ? s : s.slice(0, 120) + '...';
            }
        } catch (e) {
            result[i] = '<unserializable>';
        }
    }
    return result;
}

// Global trace function injected into every instrumented function body
global.__maplet_trace = function (name, file, line, args) {
    trySend({
        fn: name,
        file: file,
        line: line,
        args: _safeArgs(args || []),
    });
};

function isProjectFile(filename) {
    if (!filename) return false;
    return filename.startsWith(PROJECT_ROOT) &&
        !filename.includes('node_modules') &&
        !filename.includes('js_trace');
}

// Source instrumentation: inject __maplet_trace() at the start of every function body
function instrumentSource(code, filename) {
    // Patterns that indicate a function definition followed by {
    // We find the opening { and inject our trace call right after it
    const patterns = [
        // function name(...) {
        /function\s+(\w+)\s*\([^)]*\)\s*\{/g,
        // async function name(...) {
        /async\s+function\s+(\w+)\s*\([^)]*\)\s*\{/g,
        // name(...) {  (class methods)
        /(\w+)\s*\([^)]*\)\s*\{/g,
        // name = (...) => {
        /(\w+)\s*=\s*(?:async\s+)?\([^)]*\)\s*=>\s*\{/g,
        // name = function(...) {
        /(\w+)\s*=\s*(?:async\s+)?function\s*\([^)]*\)\s*\{/g,
    ];

    // Track all insertion points: { positions and their function names
    const insertions = [];
    const seen = new Set();

    for (const pattern of patterns) {
        let match;
        while ((match = pattern.exec(code)) !== null) {
            const funcName = match[1];
            // Find the { at the end of this match
            const bracePos = match.index + match[0].length - 1;

            if (seen.has(bracePos)) continue;
            seen.add(bracePos);

            // Skip common false positives
            if (['if', 'else', 'for', 'while', 'switch', 'catch', 'do'].includes(funcName)) continue;

            // Calculate line number
            const lineNum = code.substring(0, match.index).split('\n').length;

            insertions.push({
                pos: bracePos + 1, // insert after {
                funcName: funcName,
                line: lineNum
            });
        }
    }

    if (insertions.length === 0) return code;

    // Sort by position descending so insertions don't shift indices
    insertions.sort((a, b) => b.pos - a.pos);

    let result = code;
    for (const ins of insertions) {
        const traceCall = `__maplet_trace(${JSON.stringify(ins.funcName)},${JSON.stringify(filename)},${ins.line},arguments);`;
        result = result.slice(0, ins.pos) + traceCall + result.slice(ins.pos);
    }

    return result;
}

// Patch Module._compile to instrument project source files
const originalCompile = Module.prototype._compile;
Module.prototype._compile = function (content, filename) {
    if (isProjectFile(filename)) {
        try {
            content = instrumentSource(content, filename);
        } catch (e) {
            // If instrumentation fails, run uninstrumented
            process.stderr.write(`Maplet: instrumentation failed for ${filename}: ${e.message}\n`);
        }
    }
    return originalCompile.call(this, content, filename);
};

connect();
