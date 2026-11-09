<?php

namespace App;

/** Single in-memory source of users, shared by the API and web routes. */
class Users
{
    /** @return list<array{id: string, name: string}> */
    public static function fetch(): array
    {
        return [
            ['id' => '1', 'name' => 'John'],
            ['id' => '2', 'name' => 'Marya'],
        ];
    }
}
