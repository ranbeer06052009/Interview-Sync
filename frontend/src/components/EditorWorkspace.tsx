"use client";

import React, { useEffect, useRef, useState } from 'react';
import Editor, { OnMount } from '@monaco-editor/react';
import { v4 as uuidv4 } from 'uuid';

type OperationType = 'INSERT' | 'DELETE' | 'CURSOR_MOVE';

interface DocumentEvent {
  id: string;
  sessionId: string;
  userId: string;
  operation: OperationType;
  position: number;
  content: string | null;
  timestamp: string;
  version: number;
}

interface IncomingEvent {
  operation: OperationType;
  position: number;
  content: string | null;
  baseVersion: number;
}

interface Props {
  sessionId: string;
}

const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";
const WS_URL = process.env.NEXT_PUBLIC_WS_URL || "ws://localhost:8080";

// Optimized local reconstruction logic
function reconstruct(events: DocumentEvent[], maxIndex: number): string {
  const buffer: string[] = [];
  for (let i = 0; i < maxIndex; i++) {
    const e = events[i];
    if (e.operation === 'INSERT' && e.content !== null) {
      buffer.splice(e.position, 0, e.content);
    } else if (e.operation === 'DELETE') {
      buffer.splice(e.position, 1);
    }
  }
  return buffer.join('');
}

export default function EditorWorkspace({ sessionId }: Props) {
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const isRemoteEdit = useRef(false);
  const baseVersion = useRef(0);
  const userId = useRef(uuidv4());

  const [connected, setConnected] = useState(false);
  
  // Time-Travel State
  const [eventLog, setEventLog] = useState<DocumentEvent[]>([]);
  const [playbackIndex, setPlaybackIndex] = useState<number>(0);
  const [isReadOnly, setIsReadOnly] = useState<boolean>(false);

  // Anonymity & Language State
  const [isAnonymous, setIsAnonymous] = useState<boolean>(false);
  const [language, setLanguage] = useState<string>("javascript");

  // Execution State
  const [stdin, setStdin] = useState<string>("");
  const [stdout, setStdout] = useState<string>("");
  const [stderr, setStderr] = useState<string>("");
  const [isExecuting, setIsExecuting] = useState<boolean>(false);

  // Fetch initial event log and session data
  useEffect(() => {
    fetch(`${API_URL}/api/session?sessionId=${sessionId}`)
      .then(res => {
         if(res.ok) return res.json();
         return { IsAnonymous: false, Language: 'javascript' }; // fallback for dev
      })
      .then(data => {
         setIsAnonymous(data.IsAnonymous);
         setLanguage(data.Language || 'javascript');
      })
      .catch(() => console.log("Session fetch failed, using defaults"));

    fetch(`${API_URL}/api/events?sessionId=${sessionId}`)
      .then(res => res.json())
      .then((data: DocumentEvent[]) => {
        const events = data || [];
        setEventLog(events);
        setPlaybackIndex(events.length);
        if (events.length > 0) {
          baseVersion.current = events[events.length - 1].version;
        }
        
        // Reconstruct the current "live" state and inject it
        const currentDoc = reconstruct(events, events.length);
        if (editorRef.current) {
          isRemoteEdit.current = true;
          editorRef.current.setValue(currentDoc);
          isRemoteEdit.current = false;
        }
      })
      .catch(err => console.error("Failed to fetch events:", err));
  }, [sessionId]);

  const toggleAnonymity = () => {
    fetch(`${API_URL}/api/session/toggle-anonymity?sessionId=${sessionId}`, { method: 'POST' })
      .then(res => res.json())
      .then(data => setIsAnonymous(data.IsAnonymous))
      .catch(console.error);
  };

  const handleLanguageChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const newLang = e.target.value;
    setLanguage(newLang);
    fetch(`${API_URL}/api/session/language?sessionId=${sessionId}`, { 
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ language: newLang })
    }).catch(console.error);
  };

  const runCode = async () => {
    if (!editorRef.current) return;
    const code = editorRef.current.getValue();
    setIsExecuting(true);
    setStdout("");
    setStderr("");

    try {
      const res = await fetch(`${API_URL}/api/execute`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ code, language, input: stdin })
      });
      const data = await res.json();
      
      if (data.compile && data.compile.stderr) {
         setStderr(data.compile.stderr);
      } else if (data.run) {
         if (data.run.stderr) setStderr(data.run.stderr);
         if (data.run.stdout) setStdout(data.run.stdout);
      } else {
         setStderr(data.message || "Execution failed");
      }
    } catch (err) {
      setStderr("Execution failed to connect to server.");
    } finally {
      setIsExecuting(false);
    }
  };

  // Handle WebSocket Connection
  useEffect(() => {
    const ws = new WebSocket(`${WS_URL}/ws?sessionId=${sessionId}&userId=${userId.current}`);
    wsRef.current = ws;

    ws.onopen = () => setConnected(true);
    ws.onclose = () => setConnected(false);

    ws.onmessage = (event) => {
      const data: DocumentEvent = JSON.parse(event.data);
      
      setEventLog(prev => {
        const newLog = [...prev, data];
        // If the user is currently looking at the "live" state, advance the slider automatically
        setPlaybackIndex(currentIdx => {
           if (currentIdx === prev.length) {
              return newLog.length;
           }
           return currentIdx;
        });
        return newLog;
      });

      baseVersion.current = Math.max(baseVersion.current, data.version);

      if (data.userId === userId.current) return;

      if (editorRef.current && !isReadOnly) {
        const model = editorRef.current.getModel();
        if (!model) return;

        isRemoteEdit.current = true;

        if (data.operation === 'INSERT' && data.content) {
          const pos = model.getPositionAt(data.position);
          editorRef.current.executeEdits('remote', [
            {
              range: { startLineNumber: pos.lineNumber, startColumn: pos.column, endLineNumber: pos.lineNumber, endColumn: pos.column },
              text: data.content,
              forceMoveMarkers: true,
            }
          ]);
        } else if (data.operation === 'DELETE') {
          const startPos = model.getPositionAt(data.position);
          const endPos = model.getPositionAt(data.position + 1); 
          editorRef.current.executeEdits('remote', [
            {
              range: { startLineNumber: startPos.lineNumber, startColumn: startPos.column, endLineNumber: endPos.lineNumber, endColumn: endPos.column },
              text: '',
            }
          ]);
        }

        isRemoteEdit.current = false;
      }
    };

    return () => {
      ws.close();
    };
  }, [sessionId, isReadOnly]);

  // Effect to handle Time-Travel scrubbing
  useEffect(() => {
    if (!editorRef.current) return;

    if (playbackIndex < eventLog.length) {
      if (!isReadOnly) {
          setIsReadOnly(true);
          editorRef.current.updateOptions({ readOnly: true });
      }
      const historicalDoc = reconstruct(eventLog, playbackIndex);
      isRemoteEdit.current = true;
      editorRef.current.setValue(historicalDoc);
      isRemoteEdit.current = false;
    } else {
      if (isReadOnly) {
        setIsReadOnly(false);
        editorRef.current.updateOptions({ readOnly: false });
        const liveDoc = reconstruct(eventLog, eventLog.length);
        isRemoteEdit.current = true;
        editorRef.current.setValue(liveDoc);
        isRemoteEdit.current = false;
      }
    }
  }, [playbackIndex, eventLog.length, isReadOnly, eventLog]);

  const handleEditorMount: OnMount = (editor, monaco) => {
    editorRef.current = editor;

    editor.onDidChangeModelContent((e) => {
      if (isRemoteEdit.current || isReadOnly) return;

      const model = editor.getModel();
      if (!model) return;

      e.changes.forEach(change => {
        const position = model.getOffsetAt({ lineNumber: change.range.startLineNumber, column: change.range.startColumn });

        if (change.rangeLength > 0) {
          for (let i = 0; i < change.rangeLength; i++) {
            const payload: IncomingEvent = {
              operation: 'DELETE',
              position: position,
              content: null,
              baseVersion: baseVersion.current
            };
            wsRef.current?.send(JSON.stringify(payload));
          }
        }

        if (change.text.length > 0) {
          const payload: IncomingEvent = {
            operation: 'INSERT',
            position: position,
            content: change.text,
            baseVersion: baseVersion.current
          };
          wsRef.current?.send(JSON.stringify(payload));
        }
      });
    });
  };

  return (
    <div className="flex flex-col h-full bg-gray-900 text-white">
      <div className="p-4 border-b border-gray-800 flex justify-between items-center bg-gray-950">
        <div>
          <h2 className="text-lg font-bold text-gray-100">Collaborative Editor</h2>
          <div className="flex items-center gap-2 mt-1">
            <div className={`w-2 h-2 rounded-full ${connected ? 'bg-green-500' : 'bg-red-500'}`}></div>
            <span className="text-xs text-gray-400 font-medium tracking-wide uppercase">{connected ? 'Live Sync' : 'Offline'}</span>
          </div>
        </div>

        {/* Time-Travel Slider UI */}
        <div className="flex-1 max-w-xl mx-8 flex items-center gap-4 bg-gray-900 px-6 py-3 rounded-xl border border-gray-700 shadow-inner">
           <span className="text-xs font-bold text-gray-400 tracking-wider">PAST</span>
           <input 
              type="range" 
              min={0} 
              max={eventLog.length} 
              value={playbackIndex} 
              onChange={(e) => setPlaybackIndex(parseInt(e.target.value))}
              className="w-full accent-indigo-500 cursor-pointer h-2 bg-gray-700 rounded-lg appearance-none"
           />
           <span className={`text-xs font-bold tracking-wider ${playbackIndex === eventLog.length ? 'text-green-400' : 'text-gray-400'}`}>LIVE</span>
        </div>
        
        <div className="flex items-center gap-4">
          {isReadOnly && (
              <div className="px-3 py-1 bg-red-500/20 text-red-400 border border-red-500/30 rounded-md text-xs font-bold uppercase tracking-wider animate-pulse">
                Read Only (Time Travel)
              </div>
          )}
          
          <select 
            value={language}
            onChange={handleLanguageChange}
            className="bg-gray-800 text-gray-300 border border-gray-700 rounded-md px-3 py-2 text-sm font-bold outline-none cursor-pointer hover:bg-gray-700 transition-colors"
          >
            <option value="javascript">JavaScript</option>
            <option value="typescript">TypeScript</option>
            <option value="python">Python</option>
            <option value="java">Java</option>
            <option value="go">Go</option>
            <option value="cpp">C++</option>
            <option value="rust">Rust</option>
            <option value="html">HTML</option>
            <option value="css">CSS</option>
          </select>
          
          <button 
            onClick={toggleAnonymity}
            className={`px-4 py-2 rounded-md text-sm font-bold flex items-center gap-2 transition-colors ${
              isAnonymous 
                ? 'bg-purple-600 hover:bg-purple-700 text-white shadow-[0_0_15px_rgba(147,51,234,0.5)]' 
                : 'bg-gray-800 hover:bg-gray-700 text-gray-300 border border-gray-700'
            }`}
          >
            {isAnonymous ? '🕵️ Anonymous Mode ON' : '👤 Real Names ON'}
          </button>
          
          <button 
            onClick={runCode}
            disabled={isExecuting}
            className="px-4 py-2 rounded-md text-sm font-bold flex items-center gap-2 bg-green-600 hover:bg-green-500 text-white transition-colors disabled:opacity-50"
           >
            {isExecuting ? '⏳ Running...' : '▶ Run Code'}
          </button>
        </div>
      </div>
      
      {/* Editor & IO Split */}
      <div className="flex-1 flex flex-row overflow-hidden">
        <div className="flex-1 relative border-r border-gray-800">
          {isReadOnly && (
              <div className="absolute inset-0 pointer-events-none border-4 border-red-500/20 z-10"></div>
          )}
          <Editor
            height="100%"
            language={language}
            theme="vs-dark"
            onMount={handleEditorMount}
            options={{
              minimap: { enabled: false },
              fontSize: 15,
              padding: { top: 16 },
              readOnly: isReadOnly,
            }}
          />
        </div>
        
        <div className="w-[30%] min-w-[300px] flex flex-col bg-gray-950">
           <div className="flex-1 flex flex-col border-b border-gray-800">
             <div className="p-2 bg-gray-900 text-xs font-bold text-gray-400 uppercase tracking-widest border-b border-gray-800">Input.txt</div>
             <textarea 
                value={stdin}
                onChange={(e) => setStdin(e.target.value)}
                className="flex-1 bg-transparent p-3 text-sm outline-none resize-none font-mono text-gray-300"
                placeholder="Standard input..."
             />
           </div>
           <div className="flex-1 flex flex-col">
             <div className={`p-2 text-xs font-bold uppercase tracking-widest border-b border-gray-800 ${stderr ? 'bg-red-900/30 text-red-400' : 'bg-gray-900 text-gray-400'}`}>
                Output.txt {isExecuting && <span className="text-gray-500 lowercase normal-case">(running...)</span>}
             </div>
             <div className="flex-1 p-3 text-sm overflow-auto font-mono whitespace-pre-wrap">
                {stderr ? (
                  <span className="text-red-400">{stderr}</span>
                ) : (
                  <span className="text-gray-300">{stdout}</span>
                )}
             </div>
           </div>
        </div>
      </div>
    </div>
  );
}
