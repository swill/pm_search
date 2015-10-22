pm_search
=========


Setup on GeekHack
-----------------

You MUST have the following checkbox checked in your `GeekHack Personal Message Settings` in order for the tool to work (for now):

`Show most recent personal messages at top.` : `checked`

You can use any of these settings for display, but I HIGHLY recommend using the `All at once` option on the initial import to make the import faster, then you can change it back to whatever you want after.

`Display personal messages:`
- `All at once`
- `One at a time`
- `As a conversation`


Initial Run
-----------

You will need to specify a `user` the first time you run the application.  To do this, you will need to open a terminal and pass in the command line argument `-user=your_username` when you execute the binary.

### Windows
- Start > run > cmd
- Find the executable in the explorer and drag it into the open terminal.  This will copy the file path into the terminal.
- Add `-user=your_username` to that line and hit Enter.
- For a full list of commands use the `-h` argument.
- When finished, close the terminal and the application will be stopped.

### Mac
- Applications > Utilities > Terminal
- Find the executable in the explorer and drag it into the open terminal.  This will copy the file path into the terminal.
- NOTE: If the above did not put the full file path in the terminal, you will likely have to add executable rights to the binary: 
    `$ chmod +x pm_search_darwin_amd64`
- Add `-user=your_username` to that line and hit Enter.
- For a full list of commands use the `-h` argument.
- When finished, close the terminal and the application will be stopped (after you verify that you want to close).

Once entered, the `user` will be stored in a config file in the folder `.pm_search` in your HOME directory.

For convenience, you can enter password in the format `pass = yourpass` on a new line in the `.pm_search/pm_search.conf` file.  The clear text password will be removed once you run the application and a new encrypted `pass_hash` will be added to the config file.