Set fso = CreateObject("Scripting.FileSystemObject")
Set shell = CreateObject("Shell.Application")
dir = fso.GetParentFolderName(WScript.ScriptFullName)
shell.ShellExecute dir & "\tools.html", "", dir, "open", 1