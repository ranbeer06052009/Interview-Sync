import EditorWorkspace from '@/components/EditorWorkspace';

export default function Home() {
  // Hardcoded session for dev, we can pass it dynamically later
  return (
    <main className="h-screen w-full flex flex-col bg-gray-950 text-white">
      <header className="p-6 border-b border-gray-800">
        <h1 className="text-2xl font-black bg-gradient-to-r from-blue-400 to-indigo-500 bg-clip-text text-transparent">InterviewSync</h1>
        <p className="text-sm text-gray-400">Keystroke Time-Travel Platform</p>
      </header>
      
      <div className="flex-1 overflow-hidden">
        {/* Session ID must be a valid UUID for the Go backend */}
        <EditorWorkspace sessionId="123e4567-e89b-12d3-a456-426614174000" />
      </div>
    </main>
  );
}
