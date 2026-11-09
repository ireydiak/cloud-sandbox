<!DOCTYPE html>
<html>
    <head>
        <meta charset="utf-8" />
        <title>Users</title>
    </head>
    <body>
        <main>
            @foreach ($users as $user)
                <div>
                    <span>ID: {{ $user['id'] }}</span>
                    <span>Name: {{ $user['name'] }}</span>
                </div>
            @endforeach
        </main>
    </body>
</html>
