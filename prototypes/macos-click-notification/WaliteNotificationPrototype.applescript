property targetSessionID : ""

on open requestFiles
	if (count of requestFiles) is not 1 then error "Expected one iTerm2 session request file"

	set requestFile to item 1 of requestFiles
	set fileHandle to open for access requestFile
	try
		set rawSessionID to read fileHandle as «class utf8»
		close access fileHandle
	on error errorMessage number errorNumber
		try
			close access fileHandle
		end try
		error errorMessage number errorNumber
	end try

	if rawSessionID is "" then error "The iTerm2 session ID is empty"
	set rawSessionID to paragraph 1 of rawSessionID
	set targetSessionID to my normalizedSessionID(rawSessionID)

	display notification "Click this notification to restore this exact iTerm2 session." with title "Walite click prototype"
end open

on run
	if targetSessionID is "" then error "No Walite iTerm2 session has been registered"
	my restoreSession(targetSessionID)
end run

on normalizedSessionID(rawSessionID)
	set separatorOffset to offset of ":" in rawSessionID
	if separatorOffset is greater than 0 then
		set rawSessionID to text (separatorOffset + 1) thru -1 of rawSessionID
	end if
	if rawSessionID is "" then error "The iTerm2 session UUID is empty"
	return rawSessionID
end normalizedSessionID

on restoreSession(targetID)
	tell application "iTerm2"
		repeat with w in windows
			repeat with t in tabs of w
				repeat with s in sessions of t
					if (unique id of s) is targetID then
						set miniaturized of w to false
						select w
						select t
						select s
						activate
						return "Walite restored"
					end if
				end repeat
			end repeat
		end repeat
	end tell

	error "Walite session not found"
end restoreSession
